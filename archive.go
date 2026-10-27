package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"math"
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

	// maxDecoderBytes caps the window or dictionary a decompressor may allocate. It is separate from the entry cap
	// because a bigger file must not mean a bigger in-memory dictionary.
	maxDecoderBytes int64 = 256 << 20

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

// payloadBudget is everything a stream may decompress: the total cap, the
// headers the entry cap can put around it, and the peek's slack.
func (o Options) payloadBudget() int64 {
	return o.MaxTotalBytes + int64(o.MaxEntries)*tarOverhead + 1<<20
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
		*err = malformed(r)
	}
}

// malformed is what a recovered panic becomes, so every path that catches one
// reports it the same way.
func malformed(r any) error {
	return &Error{Reason: fmt.Errorf("%w: %v", ErrMalformed, r)}
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

	x := &extractor{root: dest, opts: opts.withDefaults(), seen: map[string]string{}, files: map[string]bool{}}
	x.ratioMax = x.opts.ratioCap(size)
	return walk(src, size, baseName, walkLimits{
		budget: streamBudget(x.opts.MaxTotalBytes, x.opts.MaxEntries),
		// The declared count is refused up front where a container can report
		// it; admit counts the rest as they are written.
		maxEntries: x.opts.MaxEntries,
	}, x.admitEntry)
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

// detectSource is detect over the first headLen bytes, read through a section
// reader so the caller's offset is untouched.
func detectSource(src io.ReaderAt) int {
	var magic [headLen]byte
	n, _ := io.ReadFull(io.NewSectionReader(src, 0, headLen), magic[:])
	return detect(magic[:n])
}

// item is one entry as a format reader sees it. Entry is what a caller is told;
// the rest is what Extract's policy needs and a listing has no use for.
type item struct {
	Entry
	mode      fs.FileMode
	encrypted bool
	linkType  int
	crc       *uint32
	// skip marks an entry that names no file of its own, so neither entry point
	// reports it.
	skip bool
}

// walkFunc receives one entry and a reader for its bytes. The reader is valid
// until walkFunc returns, so a caller that leaves it unread lets the decoder
// skip those bytes.
type walkFunc func(it item, open func() (io.ReadCloser, error)) error

// walkLimits are the two bounds the walk itself enforces. budget is what a
// stream may decompress; maxEntries, when non-zero, refuses a container that
// declares more entries than that before any is read, which zip and 7z can do
// and the streaming formats cannot.
type walkLimits struct {
	budget     int64
	maxEntries int
}

// errWalkStop ends a walk with no error, for a caller that already has what it
// asked for; walk itself never returns it.
var errWalkStop = errors.New("walk stopped")

// walk enumerates an archive's entries once, forward, handing each to fn.
// Extract and Lister both enumerate through it and so cannot disagree about
// what an archive holds; only their read budgets differ. budget is the payload
// a stream may decompress, which has to be set where the decoder is built
// rather than on what comes out of it.
func walk(src Source, size int64, baseName string, limits walkLimits, fn walkFunc) error {
	kind := detectSource(src)
	switch kind {
	case fmtZip:
		return walkZip(src, size, limits, fn)
	case fmtRAR:
		start, err := src.Seek(0, io.SeekStart)
		if err != nil {
			return err
		}
		return walkRAR(io.NewSectionReader(src, start, size-start), fn)
	case fmtSevenZip:
		return walkSevenZip(src, size, limits, fn)
	case fmtTar, fmtGzip, fmtXz, fmtLzma, fmtZstd, fmtBzip2:
		start, err := src.Seek(0, io.SeekStart)
		if err != nil {
			return err
		}
		return walkStream(io.NewSectionReader(src, start, size-start), kind, baseName, limits.budget, fn)
	}
	return &Error{Reason: ErrUnsupportedFormat}
}

// tooMany reports whether a container declaring n entries exceeds the cap.
func (w walkLimits) tooMany(n int) bool {
	return w.maxEntries > 0 && n > w.maxEntries
}

func walkZip(src io.ReaderAt, size int64, limits walkLimits, fn walkFunc) error {
	zr, err := zip.NewReader(src, size)
	// The path check below is stricter than the reader's own, so a reader that
	// came back anyway is still worth walking.
	if err != nil && (zr == nil || !errors.Is(err, zip.ErrInsecurePath)) {
		return wrap("", err)
	}
	if limits.tooMany(len(zr.File)) {
		return &Error{Reason: ErrTooManyEntries}
	}
	for _, f := range zr.File {
		mode := f.Mode()
		it := item{
			Entry:     Entry{Name: zipName(f), Size: declared(f.UncompressedSize64), IsDir: mode.IsDir()},
			mode:      mode,
			encrypted: f.Flags&1 != 0,
			crc:       &f.CRC32,
		}
		if err := fn(it, f.Open); err != nil {
			return err
		}
	}
	return nil
}

func walkSevenZip(src io.ReaderAt, size int64, limits walkLimits, fn walkFunc) error {
	if err := checkSevenZipLimits(src, size); err != nil {
		return wrap("", err)
	}
	zr, err := openSevenZip(src, size)
	if err != nil {
		return wrap("", err)
	}
	if limits.tooMany(len(zr.File)) {
		return &Error{Reason: ErrTooManyEntries}
	}
	for _, f := range zr.File {
		mode := f.Mode()
		it := item{
			Entry: Entry{Name: f.Name, Size: declared(f.UncompressedSize), IsDir: mode.IsDir()},
			mode:  mode,
			crc:   &f.CRC32,
		}
		if err := fn(it, f.Open); err != nil {
			return err
		}
	}
	return nil
}

func walkRAR(src io.Reader, fn walkFunc) error {
	// A larger RAR window cannot produce an entry larger than the extraction cap.
	rr, err := rardecode.NewReader(src, rardecode.MaxDictionarySize(maxDecoderBytes))
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
		it := item{
			Entry:     Entry{Name: h.Name, Size: max(h.UnPackedSize, 0), IsDir: h.IsDir},
			mode:      rarMode(h),
			encrypted: h.Encrypted || h.HeaderEncrypted,
			linkType:  int(h.LinkType),
		}
		// The reader verifies the RAR checksum itself when the entry ends.
		if err := fn(it, func() (io.ReadCloser, error) { return io.NopCloser(rr), nil }); err != nil {
			return err
		}
	}
}

func walkStream(src io.Reader, kind int, baseName string, budget int64, fn walkFunc) error {
	p, err := openPayload(src, kind, budget)
	if err != nil {
		return err
	}
	defer p.close()
	if !p.isTar {
		// A bare stream's header declares no size, so -1 marks the one entry
		// whose size only draining its bytes can give.
		it := item{Entry: Entry{Name: singleName(baseName), Size: -1}}
		return fn(it, func() (io.ReadCloser, error) { return io.NopCloser(p.r), nil })
	}
	tr := tar.NewReader(p.r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return wrap("", err)
		}
		// A pax global header describes the entries after it and names no file.
		it := item{
			Entry: Entry{Name: h.Name, Size: max(h.Size, 0), IsDir: h.Typeflag == tar.TypeDir},
			mode:  tarMode(h.Typeflag),
			skip:  h.Typeflag == tar.TypeXGlobalHeader,
		}
		if it.skip {
			continue
		}
		if err := fn(it, func() (io.ReadCloser, error) { return io.NopCloser(tr), nil }); err != nil {
			return err
		}
	}
}

// declared clamps a header's declared size, which a crafted archive can set to
// anything, into a non-negative int64.
func declared(n uint64) int64 {
	return int64(min(n, math.MaxInt64))
}

// rarMode is the header's mode plus the Unix file kind its attributes name.
// Mode() reports only the symlink, so a device, pipe or socket would otherwise
// look like a regular file.
func rarMode(h *rardecode.FileHeader) fs.FileMode {
	mode := h.Mode()
	if h.HostOS != rardecode.HostOSUnix {
		return mode
	}
	switch h.Attributes & 0xF000 {
	case 0, 0x4000, 0x8000, 0xA000:
		return mode
	case 0x1000:
		return mode | fs.ModeNamedPipe
	case 0x2000:
		return mode | fs.ModeDevice | fs.ModeCharDevice
	case 0xC000:
		return mode | fs.ModeSocket
	}
	return mode | fs.ModeIrregular
}

// tarMode is a tar header's kind as a file mode, so one kind check serves every
// format.
func tarMode(typeflag byte) fs.FileMode {
	switch typeflag {
	case tar.TypeReg, tar.TypeCont:
		return 0
	case tar.TypeDir:
		return fs.ModeDir
	case tar.TypeSymlink, tar.TypeLink:
		return fs.ModeSymlink
	case tar.TypeChar:
		return fs.ModeDevice | fs.ModeCharDevice
	case tar.TypeBlock:
		return fs.ModeDevice
	case tar.TypeFifo:
		return fs.ModeNamedPipe
	}
	return fs.ModeIrregular
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

func checkKind(name string, mode fs.FileMode) error {
	switch {
	case mode&fs.ModeSymlink != 0:
		return &Error{Entry: name, Reason: ErrLink}
	case mode&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket|fs.ModeIrregular) != 0:
		return &Error{Entry: name, Reason: ErrSpecialFile}
	}
	return nil
}

// admitEntry is extraction's policy for one walked entry: refuse encryption,
// links and special files, then admit the name before anything reaches the
// destination.
func (x *extractor) admitEntry(it item, open func() (io.ReadCloser, error)) error {
	if it.encrypted {
		return &Error{Entry: it.Name, Reason: ErrEncrypted}
	}
	if it.linkType != int(rardecode.LinkTypeNone) {
		return &Error{Entry: it.Name, Reason: ErrLink}
	}
	if err := checkKind(it.Name, it.mode); err != nil {
		return err
	}
	// A writer that separates with backslashes ends a folder's name with one too, which the reader does not see as a folder.
	isDir := it.IsDir || strings.HasSuffix(it.Name, `\`)
	rel, err := x.admit(it.Name, isDir)
	if err != nil {
		return err
	}
	if rel == "" {
		return nil
	}
	return x.entry(it, rel, isDir, open)
}

// entry creates the directory or writes the file, at the path admit returned.
func (x *extractor) entry(it item, rel string, isDir bool, open func() (io.ReadCloser, error)) error {
	target := filepath.FromSlash(rel)
	if isDir {
		if err := x.root.MkdirAll(target, 0o750); err != nil {
			return wrap(it.Name, err)
		}
		return nil
	}
	if dir := filepath.Dir(target); dir != "." {
		if err := x.root.MkdirAll(dir, 0o750); err != nil {
			return wrap(it.Name, err)
		}
	}
	src, err := open()
	if err != nil {
		return wrap(it.Name, err)
	}
	defer func() { _ = src.Close() }()
	return x.write(it.Name, target, src, it.crc)
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
