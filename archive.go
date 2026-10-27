// Package archive extracts zip, RAR, 7z, tar and gzip, xz, lzma, zstd or bzip2 files (alone or around a tar) from an
// untrusted source into a directory.
package archive

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"
)

// Default caps, applied to every zero field of Options. Thunderstore allows gigantic packages, so the byte caps sit far
// above any real one and the ratio guard, not these, stops a small archive claiming them.
const (
	DefaultMaxEntryBytes int64 = 32 << 30
	DefaultMaxTotalBytes int64 = 64 << 30
	DefaultMaxEntries          = 20000

	// MaxDecoderBytes caps the window or dictionary a decompressor may allocate. It is separate from the entry cap
	// because a bigger file must not mean a bigger in-memory dictionary.
	MaxDecoderBytes int64 = 256 << 20

	// Below DefaultRatioFloor an archive may unpack to any size within the caps; above it, to at most
	// DefaultMaxRatio times its own size. Deflate tops out near 1030:1, so real packages stay well inside.
	DefaultMaxRatio   int64 = 1024
	DefaultRatioFloor int64 = 2 << 30
)

// Options lowers or raises the extraction caps; a zero field means its default.
type Options struct {
	MaxEntryBytes int64
	MaxTotalBytes int64
	MaxEntries    int
	MaxRatio      int64
	RatioFloor    int64
	// Segment reports whether one path segment is a name the destination platform
	// will store as written. nil means WindowsSegment.
	Segment func(seg string) bool
}

// segment is the name policy cleanName applies, defaulting to WindowsSegment.
func (o Options) segment() func(seg string) bool {
	if o.Segment == nil {
		return WindowsSegment
	}
	return o.Segment
}

// ratioCap is the most an archive of srcSize bytes may unpack to, or MaxTotalBytes when its size is unknown.
func (o Options) ratioCap(srcSize int64) int64 {
	if srcSize <= 0 {
		return o.MaxTotalBytes
	}
	return min(o.MaxTotalBytes, max(o.RatioFloor, srcSize*o.MaxRatio))
}

func (o Options) withDefaults() Options {
	if o.MaxEntryBytes == 0 {
		o.MaxEntryBytes = DefaultMaxEntryBytes
	}
	if o.MaxTotalBytes == 0 {
		o.MaxTotalBytes = DefaultMaxTotalBytes
	}
	if o.MaxEntries == 0 {
		o.MaxEntries = DefaultMaxEntries
	}
	if o.MaxRatio == 0 {
		o.MaxRatio = DefaultMaxRatio
	}
	if o.RatioFloor == 0 {
		o.RatioFloor = DefaultRatioFloor
	}
	return o
}

// Source is what a reader needs. *os.File and *bytes.Reader both satisfy it.
type Source interface {
	io.ReaderAt
	io.Seeker
}

// RecoverMalformed turns a panic in a format reader, which a crafted archive can cause, into ErrMalformed for *err, so an
// untrusted download fails its item instead of ending the process.
func RecoverMalformed(err *error) {
	if r := recover(); r != nil {
		*err = &Error{Reason: fmt.Errorf("%w: %v", ErrMalformed, r)}
	}
}

// openSevenZip is sevenzip.NewReader with a panic from a crafted header returned as ErrMalformed. Reading a file from
// the result can panic too, so the caller defers RecoverMalformed as well.
func openSevenZip(r io.ReaderAt, size int64) (zr *sevenzip.Reader, err error) {
	defer RecoverMalformed(&err)
	return sevenzip.NewReader(r, size)
}

// Extract unpacks the archive src (size bytes) into dest, detecting the format
// from its magic bytes. dest must exist and is rooted by the caller. baseName
// names the single file a bare compressed stream yields. Extraction stops at
// the first failure and leaves what it wrote in dest, so the caller passes a
// temp directory and discards it on error. A zero Options field means its
// default.
func Extract(src Source, size int64, baseName string, dest *os.Root, opts Options) error {
	return extract(src, size, baseName, dest, opts)
}

// extract is Extract with caps the tests lower.
func extract(src Source, size int64, baseName string, dest *os.Root, opts Options) (err error) {
	defer RecoverMalformed(&err)

	var magic [headLen]byte
	n, _ := io.ReadFull(io.NewSectionReader(src, 0, headLen), magic[:])
	head := magic[:n]

	x := &extractor{root: dest, opts: opts.withDefaults(), seen: map[string]string{}, files: map[string]bool{}}
	x.ratioMax = x.opts.ratioCap(size)

	switch detect(head) {
	case fmtZip:
		return x.zip(src, size)
	case fmtRAR:
		start, err := src.Seek(0, io.SeekStart)
		if err != nil {
			return err
		}
		return x.rar(io.NewSectionReader(src, start, size-start))
	case fmtSevenZip:
		return x.sevenZip(src, size)
	case fmtTar, fmtGzip, fmtXz, fmtLzma, fmtZstd, fmtBzip2:
		start, err := src.Seek(0, io.SeekStart)
		if err != nil {
			return err
		}
		return x.stream(io.NewSectionReader(src, start, size-start), detect(head), baseName)
	}
	return &Error{Reason: ErrUnsupportedFormat}
}

const (
	fmtNone = iota
	fmtZip
	fmtRAR
	fmtSevenZip
	fmtTar
	fmtGzip
	fmtXz
	fmtLzma
	fmtZstd
	fmtBzip2
)

func detect(head []byte) int {
	switch {
	case bytes.HasPrefix(head, []byte("PK\x03\x04")), bytes.HasPrefix(head, []byte("PK\x05\x06")):
		return fmtZip
	case bytes.HasPrefix(head, []byte("Rar!\x1a\x07\x00")), bytes.HasPrefix(head, []byte("Rar!\x1a\x07\x01\x00")):
		return fmtRAR
	case bytes.HasPrefix(head, []byte("7z\xbc\xaf\x27\x1c")):
		return fmtSevenZip
	}
	return detectStream(head)
}

type extractor struct {
	root    *os.Root
	opts    Options
	entries int
	// ratioMax is the most the archive may unpack to for its size.
	ratioMax int64
	total    int64
	seen     map[string]string // lower-cased path -> spelling first used
	files    map[string]bool
}

func (x *extractor) zip(r io.ReaderAt, size int64) error {
	zr, err := zip.NewReader(r, size)
	// The path check below is stricter than the reader's own.
	if err != nil && (zr == nil || !errors.Is(err, zip.ErrInsecurePath)) {
		return wrap("", err)
	}
	if len(zr.File) > x.opts.MaxEntries {
		return &Error{Reason: ErrTooManyEntries}
	}
	for _, f := range zr.File {
		f.Name = zipName(f)
		mode := f.Mode()
		if f.Flags&1 != 0 {
			return &Error{Entry: f.Name, Reason: ErrEncrypted}
		}
		if err := checkKind(f.Name, mode); err != nil {
			return err
		}
		// A writer that separates with backslashes ends a folder's name with one too, which the reader does not see as a folder.
		isDir := mode.IsDir() || strings.HasSuffix(f.Name, `\`)
		if err := x.entry(f.Name, isDir, func() (io.ReadCloser, error) { return f.Open() }, &f.CRC32); err != nil {
			return err
		}
	}
	return nil
}

func (x *extractor) sevenZip(r io.ReaderAt, size int64) error {
	if err := checkSevenZipLimits(r, size); err != nil {
		return wrap("", err)
	}
	zr, err := openSevenZip(r, size)
	if err != nil {
		return wrap("", err)
	}
	if len(zr.File) > x.opts.MaxEntries {
		return &Error{Reason: ErrTooManyEntries}
	}
	for _, f := range zr.File {
		mode := f.Mode()
		if err := checkKind(f.Name, mode); err != nil {
			return err
		}
		if err := x.entry(f.Name, mode.IsDir(), f.Open, &f.CRC32); err != nil {
			return err
		}
	}
	return nil
}

func (x *extractor) rar(r io.Reader) error {
	// A larger RAR window cannot produce an entry larger than the extraction cap.
	rr, err := rardecode.NewReader(r, rardecode.MaxDictionarySize(MaxDecoderBytes))
	if err != nil {
		return wrap("", err)
	}
	for {
		h, err := rr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return wrap("", err)
		}
		if h.Encrypted || h.HeaderEncrypted {
			return &Error{Entry: h.Name, Reason: ErrEncrypted}
		}
		if h.LinkType != rardecode.LinkTypeNone {
			return &Error{Entry: h.Name, Reason: ErrLink}
		}
		mode := h.Mode()
		if h.HostOS == rardecode.HostOSUnix {
			// Mode() reports only the symlink type bit; catch devices and pipes too.
			switch h.Attributes & 0xF000 {
			case 0, 0x4000, 0x8000:
			case 0xA000:
				return &Error{Entry: h.Name, Reason: ErrLink}
			default:
				return &Error{Entry: h.Name, Reason: ErrSpecialFile}
			}
		}
		if err := checkKind(h.Name, mode); err != nil {
			return err
		}
		// The reader verifies the RAR checksum itself when the entry ends.
		open := func() (io.ReadCloser, error) { return io.NopCloser(rr), nil }
		if err := x.entry(h.Name, h.IsDir, open, nil); err != nil {
			return err
		}
	}
}

func checkKind(name string, mode fs.FileMode) error {
	switch {
	case mode&fs.ModeSymlink != 0:
		return &Error{Entry: name, Reason: ErrLink}
	case mode&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket|fs.ModeIrregular) != 0:
		return &Error{Entry: name, Reason: ErrSpecialFile}
	}
	return nil
}

// entry validates one entry's name and caps, then creates the directory or
// writes the file. wantCRC is the CRC32 the archive declares, when it has one.
func (x *extractor) entry(name string, isDir bool, open func() (io.ReadCloser, error), wantCRC *uint32) error {
	rel, err := x.admit(name, isDir)
	if err != nil {
		return err
	}
	if rel == "" {
		return nil
	}
	target := filepath.FromSlash(rel)
	if isDir {
		if err := x.root.MkdirAll(target, 0o750); err != nil {
			return wrap(name, err)
		}
		return nil
	}
	if dir := filepath.Dir(target); dir != "." {
		if err := x.root.MkdirAll(dir, 0o750); err != nil {
			return wrap(name, err)
		}
	}
	src, err := open()
	if err != nil {
		return wrap(name, err)
	}
	defer func() { _ = src.Close() }()
	return x.write(name, target, src, wantCRC)
}

func (x *extractor) write(name, target string, src io.Reader, wantCRC *uint32) error {
	// O_EXCL refuses to write through an existing symlink or over a duplicate.
	out, err := x.root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return wrap(name, err)
	}
	limit := min(x.opts.MaxEntryBytes, x.opts.MaxTotalBytes-x.total, x.ratioMax-x.total)
	h := crc32.NewIEEE()
	// One byte past the limit proves the entry is over it without trusting declared sizes.
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(src, limit+1))
	x.total += n
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return wrap(name, err)
	}
	if n > limit {
		switch {
		case n > x.opts.MaxEntryBytes:
			return &Error{Entry: name, Reason: ErrEntryTooLarge}
		case x.total > x.opts.MaxTotalBytes:
			return &Error{Entry: name, Reason: ErrArchiveTooLarge}
		}
		return &Error{Entry: name, Reason: ErrRatio}
	}
	if wantCRC != nil && h.Sum32() != *wantCRC {
		return &Error{Entry: name, Reason: ErrChecksum}
	}
	return nil
}

// admit counts the entry and returns its clean slash-separated path, or ""
// for an entry naming the destination itself.
func (x *extractor) admit(name string, isDir bool) (string, error) {
	rel, err := cleanName(name, x.opts)
	if err != nil {
		return "", &Error{Entry: name, Reason: err}
	}
	if rel == "" {
		return "", nil
	}
	x.entries++
	if x.entries > x.opts.MaxEntries {
		return "", &Error{Entry: name, Reason: ErrTooManyEntries}
	}
	segs := strings.Split(rel, "/")
	for i := range segs {
		p := strings.Join(segs[:i+1], "/")
		if prev, ok := x.seen[strings.ToLower(p)]; ok && prev != p {
			return "", &Error{Entry: name, Reason: ErrCaseCollision}
		}
		x.seen[strings.ToLower(p)] = p
	}
	if !isDir {
		if x.files[rel] {
			return "", &Error{Entry: name, Reason: ErrCaseCollision}
		}
		x.files[rel] = true
	}
	return rel, nil
}
