package archive

import (
	"fmt"
	"io"
	"sync"

	"github.com/bodgit/sevenzip"
	"github.com/klauspost/compress/zstd"
)

// RecoverMalformed turns a panic in a format reader, which a crafted archive can
// cause, into ErrMalformed for *err, so an untrusted download fails its item
// instead of ending the process.
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

// openSevenZip is sevenzip.NewReader with a panic from a crafted header returned
// as ErrMalformed. Reading a file from the result can panic too, so the caller
// defers RecoverMalformed as well.
func openSevenZip(r io.ReaderAt, size int64) (zr *sevenzip.Reader, err error) {
	defer RecoverMalformed(&err)
	registerSevenZipZstd()
	return sevenzip.NewReader(r, size)
}

// registerSevenZipZstd replaces the reader's own zstd coder, whose window is the
// library default of 512 MiB. A zstd window is declared in the frame, past what
// the header walk can see, so the bound has to sit on the decoder.
var registerSevenZipZstd = sync.OnceFunc(func() {
	sevenzip.RegisterDecompressor([]byte{0x04, 0xF7, 0x11, 0x01}, sevenZipZstd)
})

func sevenZipZstd(_ []byte, _ uint64, readers []io.ReadCloser) (io.ReadCloser, error) {
	if len(readers) != 1 {
		return nil, errSevenZipHeader
	}
	d, err := zstd.NewReader(readers[0],
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(uint64(maxDecoderBytes)),
		zstd.WithDecoderMaxWindow(uint64(maxDecoderBytes)))
	if err != nil {
		return nil, err
	}
	return zstdReadCloser{Decoder: d, src: readers[0]}, nil
}

// zstdReadCloser closes the decoder and the packed stream under it.
type zstdReadCloser struct {
	*zstd.Decoder
	src io.Closer
}

func (z zstdReadCloser) Close() error {
	z.Decoder.Close()
	return z.src.Close()
}
