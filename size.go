package archive

import (
	"archive/zip"
	"errors"
	"io"
	"math"

	"github.com/nwaples/rardecode/v2"
)

// DeclaredSize sums the uncompressed sizes the archive's headers declare. It
// is informational (an untrusted archive can lie); Extract's caps count bytes.
func DeclaredSize(src Source, size int64) (_ int64, err error) {
	defer RecoverMalformed(&err)

	var magic [headLen]byte
	n, _ := io.ReadFull(io.NewSectionReader(src, 0, headLen), magic[:])

	var total int64
	switch detect(magic[:n]) {
	case fmtZip:
		zr, err := zip.NewReader(src, size)
		if err != nil && (zr == nil || !errors.Is(err, zip.ErrInsecurePath)) {
			return 0, wrap("", err)
		}
		for _, e := range zr.File {
			total += int64(min(e.UncompressedSize64, math.MaxInt64))
		}
	case fmtSevenZip:
		zr, err := openSevenZip(src, size)
		if err != nil {
			return 0, wrap("", err)
		}
		for _, e := range zr.File {
			total += int64(min(e.UncompressedSize, math.MaxInt64))
		}
	case fmtRAR:
		start, err := src.Seek(0, io.SeekStart)
		if err != nil {
			return 0, err
		}
		// The reader allocates the window on Next() and compares it against a 4 GiB default, so a crafted header
		// forces the allocation before a byte is read and no reader cap sits below it to prevent that.
		rr, err := rardecode.NewReader(io.NewSectionReader(src, start, size-start), rardecode.MaxDictionarySize(maxDecoderBytes))
		if err != nil {
			return 0, wrap("", err)
		}
		for {
			h, err := rr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return 0, wrap("", err)
			}
			total += h.UnPackedSize
		}
	case fmtTar, fmtGzip, fmtXz, fmtLzma, fmtZstd, fmtBzip2:
		start, err := src.Seek(0, io.SeekStart)
		if err != nil {
			return 0, err
		}
		return streamSize(io.NewSectionReader(src, start, size-start), detect(magic[:n]), size)
	default:
		return 0, &Error{Reason: ErrUnsupportedFormat}
	}
	return total, nil
}
