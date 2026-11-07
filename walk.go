package archive

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"math"
	"strings"

	"github.com/Rethunk-Tech/go-archive/internal/rardecode"
	"github.com/bodgit/sevenzip"
	"github.com/ulikunitz/xz/lzma"
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
	fn := backslashDirs(visit)
	switch kind := detectSource(src, baseName); kind {
	case fmtZip:
		return walkZip(src, size, limits, fn)
	case fmtSevenZip:
		return walkSevenZip(src, size, limits, fn)
	default:
		return walkForward(io.NewSectionReader(src, 0, size), kind, baseName, limits, fn)
	}
}

// walkForward walks the formats a forward-only reader can hold: RAR and the
// streams. Zip and 7z need random access and are refused here.
func walkForward(r io.Reader, kind int, baseName string, limits walkLimits, fn walkFunc) error {
	switch kind {
	case fmtRAR:
		return walkRAR(r, limits, fn)
	case fmtTar, fmtGzip, fmtXz, fmtLzma, fmtZstd, fmtBzip2, fmtLz4:
		return walkStream(r, kind, baseName, limits, fn)
	}
	return &Error{Reason: ErrUnsupportedFormat}
}

// backslashDirs marks an entry whose name ends in a backslash as a folder: a
// writer that separates with backslashes ends a folder's name with one too,
// which the reader does not see as a folder.
func backslashDirs(visit walkFunc) walkFunc {
	return func(it item, open func() (io.ReadCloser, error)) error {
		if strings.HasSuffix(it.Name, `\`) {
			it.IsDir = true
		}
		return visit(it, open)
	}
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
	if err := checkZipDirectory(src, size, limits); err != nil {
		return err
	}
	zr, err := zip.NewReader(src, size)
	// The path check below is stricter than the reader's own, so a reader that
	// came back anyway is still worth walking.
	if err != nil && (zr == nil || !errors.Is(err, zip.ErrInsecurePath)) {
		return wrap("", err)
	}
	if limits.tooMany(len(zr.File)) {
		return &Error{Reason: ErrTooManyEntries}
	}
	for method, kind := range zipMethods {
		zr.RegisterDecompressor(method, zipDecompressor(kind))
	}
	for _, f := range zr.File {
		mode := f.Mode()
		it := item{
			Name: utf8Name(f.Name), Size: declared(f.UncompressedSize64), IsDir: mode.IsDir(),
			mode:      mode,
			encrypted: f.Flags&1 != 0,
			crc:       &f.CRC32,
		}
		open := f.Open
		if f.Method == zipLZMA {
			open = func() (io.ReadCloser, error) { return openZipLZMA(f) }
		}
		if err := fn(it, open); err != nil {
			return err
		}
	}
	return nil
}

const (
	zipEOCDSize      = 22
	zipEOCDSig       = 0x06054b50
	zip64LocatorSize = 20
	zip64LocatorSig  = 0x07064b50
	zip64EOCDSig     = 0x06064b50
	zip64EOCDMinSize = 56
	zipMaxComment    = 1<<16 - 1
	zipMinRecord     = 46 // fixed part of a central-directory record
	zipCDSig         = 0x02014b50
)

// checkZipDirectory refuses a zip whose end-of-central-directory record declares more entries than the entry cap allows,
// or whose directory spans more bytes than that many records could fill. archive/zip builds every record in memory before a caller can count them, so
// the check has to come first. Anything it cannot read is left for zip.NewReader to refuse.
func checkZipDirectory(src io.ReaderAt, size int64, limits walkLimits) error {
	if limits.maxEntries <= 0 || size < zipEOCDSize {
		return nil
	}
	tail := min(size, zipEOCDSize+zipMaxComment)
	buf := make([]byte, tail)
	if _, err := src.ReadAt(buf, size-tail); err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	i := bytes.LastIndex(buf, []byte{'P', 'K', 5, 6})
	if i < 0 || len(buf)-i < zipEOCDSize {
		return nil
	}
	eocd := buf[i:]
	if binary.LittleEndian.Uint32(eocd) != zipEOCDSig {
		return nil
	}
	count := uint64(binary.LittleEndian.Uint16(eocd[10:]))
	dirSize := uint64(binary.LittleEndian.Uint32(eocd[12:]))
	dirOffset := uint64(binary.LittleEndian.Uint32(eocd[16:]))
	eocdAt := size - tail + int64(i)
	if count == 0xFFFF || dirSize == 0xFFFFFFFF || dirOffset == 0xFFFFFFFF {
		if c, d, o, ok := zip64Counts(src, size, eocdAt); ok {
			count, dirSize, dirOffset = c, d, o
		}
	}
	if count > uint64(limits.maxEntries) {
		return &Error{Reason: ErrTooManyEntries}
	}
	// archive/zip reads records from the directory's start until one is not a record, whatever the end record's count
	// and size say, so the claim bounds nothing: the records themselves are counted.
	if countRecords(src, directoryStart(src, eocdAt, dirSize, dirOffset), eocdAt, limits.maxEntries) > limits.maxEntries {
		return &Error{Reason: ErrTooManyEntries}
	}
	return nil
}

// countRecords counts central-directory records from start, stopping at the end record or once there are more than
// stopAfter.
func countRecords(src io.ReaderAt, start, end int64, stopAfter int) int {
	r := bufio.NewReaderSize(io.NewSectionReader(src, start, end-start), 64<<10)
	var head [zipMinRecord]byte
	n := 0
	for n <= stopAfter {
		if _, err := io.ReadFull(r, head[:]); err != nil || binary.LittleEndian.Uint32(head[:]) != zipCDSig {
			break
		}
		n++
		skip := int(binary.LittleEndian.Uint16(head[28:])) + int(binary.LittleEndian.Uint16(head[30:])) + int(binary.LittleEndian.Uint16(head[32:]))
		if _, err := r.Discard(skip); err != nil {
			break
		}
	}
	return n
}

// directoryStart is where archive/zip begins reading central-directory records: the end record's offset shifted by
// whatever precedes the archive, unless a record sits at the plain offset.
func directoryStart(src io.ReaderAt, eocdAt int64, dirSize, dirOffset uint64) int64 {
	start := eocdAt - int64(min(dirSize, math.MaxInt64>>1))
	off := int64(min(dirOffset, math.MaxInt64>>1))
	if start < 0 || start > eocdAt {
		return 0
	}
	if start > off {
		var sig [4]byte
		if !readFull(src, sig[:], off) && binary.LittleEndian.Uint32(sig[:]) == zipCDSig {
			return off
		}
	}
	return start
}

// zip64Counts reads the entry count and directory size from the zip64 end record the locator before the end record
// at eocdAt points to.
func zip64Counts(src io.ReaderAt, size, eocdAt int64) (count, dirSize, dirOffset uint64, ok bool) {
	var loc [zip64LocatorSize]byte
	if eocdAt < zip64LocatorSize || readFull(src, loc[:], eocdAt-zip64LocatorSize) ||
		binary.LittleEndian.Uint32(loc[:]) != zip64LocatorSig {
		return 0, 0, 0, false
	}
	off := int64(min(binary.LittleEndian.Uint64(loc[8:]), math.MaxInt64))
	if off > size {
		return 0, 0, 0, false
	}
	var rec [zip64EOCDMinSize]byte
	if readFull(src, rec[:], off) || binary.LittleEndian.Uint32(rec[:]) != zip64EOCDSig {
		return 0, 0, 0, false
	}
	return binary.LittleEndian.Uint64(rec[32:]), binary.LittleEndian.Uint64(rec[40:]), binary.LittleEndian.Uint64(rec[48:]), true
}

// readFull reports whether reading len(p) bytes at off failed.
func readFull(src io.ReaderAt, p []byte, off int64) bool {
	n, err := src.ReadAt(p, off)
	return n < len(p) && err != nil
}

const zipLZMA = 14

// openZipLZMA reads a zip LZMA entry, which archive/zip cannot: a stream
// written without an end marker stops at the entry's size, and the zip package
// tells a decompressor no size. The CRC is checked where the bytes are written.
func openZipLZMA(f *zip.File) (io.ReadCloser, error) {
	raw, err := f.OpenRaw()
	if err != nil {
		return nil, err
	}
	// Two version bytes, the properties' length, then the five properties.
	var head [9]byte
	if _, err := io.ReadFull(raw, head[:]); err != nil || binary.LittleEndian.Uint16(head[2:4]) != 5 {
		return nil, ErrMalformed
	}
	props := head[4:9]
	if binary.LittleEndian.Uint32(props[1:]) > uint32(maxDecoderBytes) {
		return nil, ErrDecoderMemory
	}
	size := uint64(math.MaxUint64) // unknown: the general flag's bit 1 says a marker ends it
	if f.Flags&2 == 0 {
		size = f.UncompressedSize64
	}
	classic := binary.LittleEndian.AppendUint64(bytes.Clone(props), size)
	lr, err := lzma.NewReader(io.MultiReader(bytes.NewReader(classic), raw))
	if err != nil {
		return nil, err
	}
	return io.NopCloser(lr), nil
}

func walkSevenZip(src io.ReaderAt, size int64, limits walkLimits, fn walkFunc) error {
	encrypted, err := checkSevenZipLimits(src, size, limits.maxEntries)
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
	p, err := openPayload(src, kind, baseName, limits)
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
