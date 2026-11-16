package archive

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"slices"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/ulikunitz/xz"
	"github.com/ulikunitz/xz/lzma"
)

// headLen is how much of a file's start detect reads: enough for a tar's
// two-block end marker, which also covers the "ustar" marker at offset 257 of
// a header.
const headLen = 1024

// tarOverhead is what a tar adds around each entry's bytes (header, padding),
// added to the stream's budget as the walk reads each header, so skipped or
// unread data cannot decompress without bound.
const tarOverhead = 1536

// streamBudget is the most a stream may decompress before any tar header is
// seen: the payload allowance and a megabyte for the peek. A tar's headers are
// not payload, so charging them to the allowance would stop a legitimate
// archive short of it; payload.grantHeader grants them as they are read.
func streamBudget(payload int64) int64 {
	return saturatingAdd(payload, 1<<20)
}

// headerAllowance is the most tar overhead a stream may add to its budget: what
// the entry cap could hold. It is a ceiling spent header by header, never a
// grant, so an archive with few entries cannot decompress the whole of it.
func headerAllowance(entries int) int64 {
	// Options fills a zero cap with its default before a stream is walked, so a
	// zero here grants no framing at all rather than an unbounded amount.
	if entries <= 0 {
		return 0
	}
	// An entry cap near MaxInt would wrap the product negative and shrink the budget.
	const most = math.MaxInt64 - 1<<20
	if int64(entries) > most/tarOverhead {
		return most
	}
	return int64(entries) * tarOverhead
}

// looksLikeLZMA recognises the classic .lzma header, which has no magic: valid
// properties, a dictionary size an encoder would write (a power of two, or one
// and a half of one) and an unknown or sane uncompressed size.
func looksLikeLZMA(head []byte) bool {
	if len(head) < lzma.HeaderLen || head[0] >= 225 {
		return false
	}
	dict := binary.LittleEndian.Uint32(head[1:5])
	pow2 := func(v uint32) bool { return v != 0 && v&(v-1) == 0 }
	if dict < 1<<12 || (!pow2(dict) && (dict%3 != 0 || !pow2(dict/3))) {
		return false
	}
	size := binary.LittleEndian.Uint64(head[5:13])
	return size == math.MaxUint64 || size < 1<<50
}

func detectStream(head []byte) int {
	switch {
	case bytes.HasPrefix(head, []byte{0x1f, 0x8b}):
		return fmtGzip
	case bytes.HasPrefix(head, []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}):
		return fmtXz
	case bytes.HasPrefix(head, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		return fmtZstd
	case len(head) >= 4 && bytes.HasPrefix(head, []byte("BZh")) && head[3] >= '1' && head[3] <= '9':
		return fmtBzip2
	case bytes.HasPrefix(head, []byte{0x04, 0x22, 0x4D, 0x18}):
		return fmtLz4
	case isTarHeader(head):
		return fmtTar
	case looksLikeLZMA(head):
		return fmtLzma
	}
	return fmtNone
}

func isTarHeader(b []byte) bool {
	if len(b) < 512 {
		return false
	}
	var unsigned, signed int64
	for i, c := range b[:512] {
		if i >= 148 && i < 156 {
			c = ' '
		}
		unsigned += int64(c)
		signed += int64(int8(c))
	}
	n, ok := tarOctal(b[148:156])
	return ok && (n == unsigned || n == signed)
}

func tarOctal(b []byte) (int64, bool) {
	i, j := 0, len(b)
	for i < j && (b[i] == 0 || b[i] == ' ') {
		i++
	}
	for j > i && (b[j-1] == 0 || b[j-1] == ' ') {
		j--
	}
	if i == j {
		return 0, true
	}
	var n int64
	for _, c := range b[i:j] {
		if c < '0' || c > '7' {
			return 0, false
		}
		n = n*8 + int64(c-'0')
	}
	return n, true
}

// emptyTar reports a tar holding no entries: only the end marker of two zero
// blocks. The name has to say tar as well, since a file of zeros is otherwise a
// valid stream payload (the zero bomb) and no header says otherwise.
func emptyTar(head []byte, baseName string) bool {
	if len(head) < headLen || slices.ContainsFunc(head[:headLen], func(b byte) bool { return b != 0 }) {
		return false
	}
	return isTarName(baseName)
}

// limitReader fails with reason once more than its budget has been read,
// whatever the stream claims.
type limitReader struct {
	r      io.Reader
	left   int64
	reason error
	// headers is the framing a tar may still add to left, spent one header at a
	// time so the grant matches what the archive holds rather than its cap.
	headers int64
}

// grant raises the ceiling by n, for framing bytes a format adds around the
// payload it declared.
func (c *limitReader) grant(n int64) {
	c.left = saturatingAdd(c.left, n)
}

func (c *limitReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		var one [1]byte
		if n, err := c.r.Read(one[:]); n > 0 {
			return 0, &Error{Reason: c.reason}
		} else if err != nil {
			return 0, err
		}
		return 0, nil
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

// Close releases the reader underneath, so a bound can wrap one directly.
func (c *limitReader) Close() error {
	if rc, ok := c.r.(io.Closer); ok {
		return rc.Close()
	}
	return nil
}

// payload is the decompressed bytes of a stream format, positioned at the start,
// and whether they are a tar.
type payload struct {
	r     *bufio.Reader
	limit *limitReader
	isTar bool
	close func()
}

// grantHeader spends one entry's worth of the tar framing allowance on the
// header the next tar.Next reads. The allowance is a ceiling spent as headers
// appear, so an archive holding a few entries cannot decompress a whole entry
// cap worth of headers it does not have.
func (p *payload) grantHeader() {
	if p.limit == nil || p.limit.headers <= 0 {
		return
	}
	n := min(tarOverhead, p.limit.headers)
	p.limit.headers -= n
	p.limit.grant(n)
}

// openPayload decompresses r (already at the file's start) as kind, allowing
// limits.budget bytes of decompressed output. The cap goes on the decoder here
// rather than on what comes out of it, so a stream bomb stops at the bound
// instead of decompressing unbounded and being rejected afterwards.
func openPayload(r io.Reader, kind int, baseName string, limits walkLimits) (*payload, error) {
	plain, closeFn, err := decoder(r, kind)
	if err != nil {
		return nil, wrap("", err)
	}
	lr := &limitReader{r: plain, left: limits.budget, reason: limits.over, headers: limits.headers}
	br := bufio.NewReaderSize(lr, 4096)
	head, err := br.Peek(headLen)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) && len(head) == 0 {
		closeFn()
		return nil, wrap("", err)
	}
	return &payload{r: br, limit: lr, isTar: kind == fmtTar || isTarHeader(head) || emptyTar(head, baseName), close: closeFn}, nil
}

// decoder decompresses r as kind, with the window or dictionary held to
// maxDecoderBytes, and returns what releases it.
func decoder(r io.Reader, kind int) (io.Reader, func(), error) {
	switch kind {
	case fmtGzip:
		zr, err := gzip.NewReader(r)
		if err != nil {
			return nil, nil, err
		}
		return zr, func() { _ = zr.Close() }, nil
	case fmtXz:
		xr, err := xz.ReaderConfig{DictCap: int(maxDecoderBytes)}.NewReader(r)
		return xr, func() {}, err
	case fmtLzma:
		lr, err := lzma.ReaderConfig{DictCap: int(maxDecoderBytes)}.NewReader(r)
		return lr, func() {}, err
	case fmtZstd:
		zr, err := zstd.NewReader(r,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderMaxMemory(uint64(maxDecoderBytes)),
			zstd.WithDecoderMaxWindow(uint64(maxDecoderBytes)))
		if err != nil {
			return nil, nil, err
		}
		return zr, zr.Close, nil
	case fmtBzip2:
		return bzip2.NewReader(r), func() {}, nil
	case fmtLz4:
		// A frame's blocks are at most 4 MiB, so the decoder needs no bound of its own.
		return lz4.NewReader(r), func() {}, nil
	}
	return r, func() {}, nil
}

// zipMethods are the zip compression methods beyond Deflate that 7-Zip and
// WinZip write and a stream decoder here already reads.
var zipMethods = map[uint16]int{12: fmtBzip2, 93: fmtZstd, 95: fmtXz}

// zipDecompressor adapts decoder to archive/zip, which wants a reader back
// whatever happens; a decoder that cannot start fails on the first Read.
func zipDecompressor(kind int) zip.Decompressor {
	return func(r io.Reader) io.ReadCloser {
		plain, closeFn, err := decoder(r, kind)
		return zipDecoder{r: plain, close: closeFn, err: err}
	}
}

type zipDecoder struct {
	r     io.Reader
	close func()
	err   error
}

func (d zipDecoder) Read(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	return d.r.Read(p)
}

func (d zipDecoder) Close() error {
	if d.close != nil {
		d.close()
	}
	return nil
}

// singleName is the name a bare compressed file lists under: the name the
// caller supplied, without its stream suffix when it has one.
func singleName(baseName string) string {
	name := stripBareSuffix(baseName)
	if name == "" || name == "." {
		return "file"
	}
	return name
}

// streamSize is the bytes a stream format unpacks to, counted by reading it
// within the default caps; a stream over them reports the cap.
func streamSize(r io.Reader, kind int, srcSize int64) (int64, error) {
	opts := Options{}.withDefaults()
	p, err := openPayload(r, kind, "", opts.streamLimits(srcSize))
	if err != nil {
		return 0, err
	}
	defer p.close()
	n, err := io.Copy(io.Discard, p.r)
	if _, typed := errors.AsType[*Error](err); typed {
		return opts.ratioCap(srcSize), nil
	}
	return n, err
}
