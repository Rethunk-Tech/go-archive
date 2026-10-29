package archive

import (
	"archive/tar"
	"archive/zip"
	"errors"
	"io"
	"io/fs"
	"math"
	"strings"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"
)

// item is one entry as a format reader sees it. Entry is what a caller is told;
// the rest is what Extract's policy needs and a listing has no use for.
type item struct {
	Entry
	mode      fs.FileMode
	encrypted bool
	linkType  int
	crc       *uint32
}

// walkFunc receives one entry and a reader for its bytes. The reader is valid
// until walkFunc returns, so a caller that leaves it unread lets the decoder
// skip those bytes.
type walkFunc func(it item, open func() (io.ReadCloser, error)) error

// walkLimits are the bounds the walk itself enforces. budget is what a stream
// may decompress, and over is the reason it fails with past that; maxEntries,
// when non-zero, refuses a container that declares more entries than that
// before any is read, which zip and 7z can do and the streaming formats cannot.
type walkLimits struct {
	budget     int64
	over       error
	maxEntries int
}

// errWalkStop ends a walk with no error, for a caller that already has what it
// asked for; walk itself never returns it.
var errWalkStop = errors.New("walk stopped")

// walk enumerates an archive's entries once, forward, handing each to fn.
// Extract and Lister both enumerate through it and so cannot disagree about
// what an archive holds; only their bounds and reads differ. The stream budget
// has to be set where the decoder is built rather than on what comes out of it.
func walk(src Source, size int64, baseName string, limits walkLimits, visit walkFunc) error {
	// A writer that separates with backslashes ends a folder's name with one too,
	// which the reader does not see as a folder.
	fn := func(it item, open func() (io.ReadCloser, error)) error {
		if strings.HasSuffix(it.Name, `\`) {
			it.IsDir = true
		}
		return visit(it, open)
	}
	kind := detectSource(src)
	switch kind {
	case fmtZip:
		return walkZip(src, size, limits, fn)
	case fmtRAR:
		return walkRAR(io.NewSectionReader(src, 0, size), limits, fn)
	case fmtSevenZip:
		return walkSevenZip(src, size, limits, fn)
	case fmtTar, fmtGzip, fmtXz, fmtLzma, fmtZstd, fmtBzip2:
		return walkStream(io.NewSectionReader(src, 0, size), kind, baseName, limits, fn)
	}
	return &Error{Reason: ErrUnsupportedFormat}
}

// tooMany reports whether a container declaring n entries exceeds the cap.
func (w walkLimits) tooMany(n int) bool {
	return w.maxEntries > 0 && n > w.maxEntries
}

// solidCharge counts the bytes a solid 7z or RAR reader decompresses to reach an
// entry: the declared sizes of every entry before it, which it decodes and
// discards whether or not they were read. Charging them to the stream budget
// bounds skipped entries the way the budget bounds a stream's.
type solidCharge struct {
	limits walkLimits
	total  int64
}

func (s *solidCharge) add(it item) error {
	s.total = saturatingAdd(s.total, it.Size)
	if s.total > s.limits.budget {
		return &Error{Entry: it.Name, Reason: s.limits.over}
	}
	return nil
}

// saturatingAdd is a + b for non-negative sizes, stopping at MaxInt64.
func saturatingAdd(a, b int64) int64 {
	if b > math.MaxInt64-a {
		return math.MaxInt64
	}
	return a + b
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
			Name: utf8Name(f.Name), Size: declared(f.UncompressedSize64), IsDir: mode.IsDir(),
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
	encrypted, err := checkSevenZipLimits(src, size)
	if err != nil {
		return wrap("", err)
	}
	zr, err := openSevenZip(src, size)
	if err != nil {
		return wrap("", err)
	}
	if limits.tooMany(len(zr.File)) {
		return &Error{Reason: ErrTooManyEntries}
	}
	charge := solidCharge{limits: limits}
	folders := sevenZipFolders{files: zr.File, current: -1}
	for i, f := range zr.File {
		mode := f.Mode()
		it := item{
			Name: f.Name, Size: declared(f.UncompressedSize), IsDir: mode.IsDir(),
			mode: mode,
			// The reader marks no entry; any encrypted folder refuses them all.
			encrypted: encrypted,
		}
		// The reader reports a missing digest as zero.
		if f.CRC32 != 0 {
			it.crc = &f.CRC32
		}
		if err := charge.add(it); err != nil {
			return err
		}
		if err := folders.enter(i); err != nil {
			return err
		}
		if err := fn(it, folders.open(i)); err != nil {
			return err
		}
	}
	return folders.leave()
}

// sevenZipFolders closes each folder's decoder as the walk leaves it. The reader
// keeps a decoder, dictionary and all, for any folder left partway through,
// until the archive is dropped; reading the folder's last entry to its end
// closes it instead. That reading is the solid decoding solidCharge counted.
type sevenZipFolders struct {
	files   []*sevenzip.File
	current int // folder of the entries being walked, -1 before the first
	last    int // index of the current folder's last entry with data
	opened  bool
	done    bool // the last entry was opened, so closing it ends the folder
}

func holdsData(f *sevenzip.File) bool { return f.UncompressedSize > 0 && !f.Mode().IsDir() }

// enter moves to entry i, finishing the previous folder when i starts another.
func (s *sevenZipFolders) enter(i int) error {
	f := s.files[i]
	if !holdsData(f) || f.Stream == s.current {
		return nil
	}
	if err := s.leave(); err != nil {
		return err
	}
	s.current, s.last, s.opened, s.done = f.Stream, i, false, false
	for j := i + 1; j < len(s.files) && (!holdsData(s.files[j]) || s.files[j].Stream == f.Stream); j++ {
		if holdsData(s.files[j]) {
			s.last = j
		}
	}
	return nil
}

// open is entry i's opener, whose reader reads to the entry's end on Close so
// the decoder moves on to the next entry rather than staying parked mid-file.
func (s *sevenZipFolders) open(i int) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		rc, err := s.files[i].Open()
		if err != nil {
			return nil, err
		}
		if holdsData(s.files[i]) {
			s.opened = true
			s.done = s.done || i == s.last
		}
		return drainOnClose{rc}, nil
	}
}

// leave reads the current folder's last entry to its end when an earlier entry
// left the decoder parked.
func (s *sevenZipFolders) leave() error {
	if !s.opened || s.done {
		return nil
	}
	s.done = true
	f := s.files[s.last]
	rc, err := f.Open()
	if err != nil {
		return wrap(f.Name, err)
	}
	if err := (drainOnClose{rc}).Close(); err != nil {
		return wrap(f.Name, err)
	}
	return nil
}

type drainOnClose struct{ io.ReadCloser }

func (d drainOnClose) Close() error {
	_, err := io.Copy(io.Discard, d.ReadCloser)
	if cerr := d.ReadCloser.Close(); err == nil {
		err = cerr
	}
	return err
}

func walkRAR(src io.Reader, limits walkLimits, fn walkFunc) error {
	// A larger RAR window cannot produce an entry larger than the extraction cap.
	rr, err := rardecode.NewReader(src, rardecode.MaxDictionarySize(maxDecoderBytes))
	if err != nil {
		return wrap("", err)
	}
	charge := solidCharge{limits: limits}
	for {
		h, err := rr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return wrap("", err)
		}
		it := item{
			Name: h.Name, Size: max(h.UnPackedSize, 0), IsDir: h.IsDir,
			mode:      rarMode(h),
			encrypted: h.Encrypted || h.HeaderEncrypted,
			linkType:  int(h.LinkType),
		}
		if err := charge.add(it); err != nil {
			return err
		}
		// The reader verifies the RAR checksum itself when the entry ends.
		if err := fn(it, func() (io.ReadCloser, error) { return io.NopCloser(rr), nil }); err != nil {
			return err
		}
	}
}

func walkStream(src io.Reader, kind int, baseName string, limits walkLimits, fn walkFunc) error {
	p, err := openPayload(src, kind, limits)
	if err != nil {
		return err
	}
	defer p.close()
	if !p.isTar {
		// A bare stream's header declares no size, so -1 marks the one entry
		// whose size only draining its bytes can give.
		it := item{Name: singleName(baseName), Size: -1}
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
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		it := item{
			Name: h.Name, Size: max(h.Size, 0), IsDir: h.Typeflag == tar.TypeDir,
			mode: tarMode(h.Typeflag),
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
	case tar.TypeReg, tar.TypeCont, tar.TypeGNUSparse:
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
