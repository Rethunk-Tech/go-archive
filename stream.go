package archive

import (
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
	"github.com/ulikunitz/xz/lzma"
)

// headLen is how much of a file's start detect reads: enough for the "ustar"
// marker at offset 257 of a tar header.
const headLen = 262

// dictLimit caps a decompressor's window, so a header cannot make it allocate
// more than maxDecoderBytes.
const dictLimit = maxDecoderBytes

// tarOverhead is what a tar adds around each entry's bytes (header, padding),
// counted into the stream's budget so skipped or unread data cannot decompress
// without bound.
const tarOverhead = 1536

// streamBudget is the most a stream may decompress for a payload allowance: the
// allowance, the tar headers around it, and a megabyte for the peek. A tar's
// headers are not payload, so charging them to the allowance would stop a
// legitimate archive short of it.
func streamBudget(payload int64, entries int) int64 {
	return payload + int64(entries)*tarOverhead + 1<<20
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
	case isTarHeader(head):
		return fmtTar
	case looksLikeLZMA(head):
		return fmtLzma
	}
	return fmtNone
}

func isTarHeader(head []byte) bool {
	return len(head) >= 262 && bytes.Equal(head[257:262], []byte("ustar"))
}

// limitReader fails with reason once more than its budget has been read,
// whatever the stream claims.
type limitReader struct {
	r      io.Reader
	left   int64
	reason error
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

// capReader bounds a whole stream's decompressed output.
func capReader(r io.Reader, budget int64) *limitReader {
	return &limitReader{r: r, left: budget, reason: ErrArchiveTooLarge}
}

// payload is the decompressed bytes of a stream format, positioned at the start,
// and whether they are a tar.
type payload struct {
	r     *bufio.Reader
	isTar bool
	close func()
}

// openPayload decompresses r (already at the file's start) as kind, allowing
// budget bytes of decompressed output. The cap goes on the decoder here rather
// than on what comes out of it, so a stream bomb stops at the bound instead of
// decompressing unbounded and being rejected afterwards.
func openPayload(r io.Reader, kind int, budget int64) (*payload, error) {
	closeFn := func() {}
	var plain io.Reader
	switch kind {
	case fmtTar:
		plain = r
	case fmtGzip:
		zr, err := gzip.NewReader(r)
		if err != nil {
			return nil, wrap("", err)
		}
		plain, closeFn = zr, func() { _ = zr.Close() }
	case fmtXz:
		xr, err := xz.ReaderConfig{DictCap: int(dictLimit)}.NewReader(r)
		if err != nil {
			return nil, wrap("", err)
		}
		plain = xr
	case fmtLzma:
		lr, err := lzma.ReaderConfig{DictCap: int(dictLimit)}.NewReader(r)
		if err != nil {
			return nil, wrap("", err)
		}
		plain = lr
	case fmtZstd:
		zr, err := zstd.NewReader(r,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderMaxMemory(uint64(dictLimit)),
			zstd.WithDecoderMaxWindow(uint64(dictLimit)))
		if err != nil {
			return nil, wrap("", err)
		}
		plain, closeFn = zr, zr.Close
	case fmtBzip2:
		plain = bzip2.NewReader(r)
	}
	br := bufio.NewReaderSize(capReader(plain, budget), 4096)
	head, err := br.Peek(512)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) && len(head) == 0 {
		closeFn()
		return nil, wrap("", err)
	}
	return &payload{r: br, isTar: kind == fmtTar || isTarHeader(head), close: closeFn}, nil
}

// singleName is the name a bare compressed file lists under: the name the
// caller supplied, without its stream suffix when it has one.
func singleName(baseName string) string {
	if _, original, ok := strings.Cut(baseName, "~~"); ok {
		baseName = original
	}
	name := stripBareSuffix(baseName)
	if name == "" || name == "." {
		return "file"
	}
	return name
}

// streamOptions are the caps for reading a stream of srcSize bytes without extracting it.
func streamOptions(srcSize int64) Options {
	o := Options{MaxEntries: DefaultMaxEntries}.withDefaults()
	o.MaxTotalBytes = o.ratioCap(srcSize)
	return o
}

// streamSize is the bytes a stream format unpacks to, counted by reading it
// within the caps; a stream over them reports the cap.
func streamSize(r io.Reader, kind int, srcSize int64) (int64, error) {
	opts := streamOptions(srcSize)
	p, err := openPayload(r, kind, streamBudget(opts.MaxTotalBytes, opts.MaxEntries))
	if err != nil {
		return 0, err
	}
	defer p.close()
	n, err := io.Copy(io.Discard, p.r)
	if _, typed := errors.AsType[*Error](err); typed {
		return opts.MaxTotalBytes, nil
	}
	return n, err
}
