package archive

import (
	"io"
	"math"
)

// DeclaredSize sums the uncompressed sizes the archive's headers declare. It
// is informational (an untrusted archive can lie); Extract's caps count bytes.
// A stream declares no size, so it is read within the default caps and one over
// them reports the cap.
func DeclaredSize(src Source, size int64) (_ int64, err error) {
	defer RecoverMalformed(&err)

	switch kind := detectSource(src, ""); kind {
	case fmtTar, fmtGzip, fmtXz, fmtLzma, fmtZstd, fmtBzip2, fmtLz4:
		return streamSize(io.NewSectionReader(src, 0, size), kind, size)
	}
	var total int64
	// Nothing is opened, so only a solid RAR decodes what it skips; the walk
	// still runs the 7z header checks and the RAR dictionary cap.
	limits := walkLimits{budget: math.MaxInt64, over: ErrArchiveTooLarge}
	err = walk(src, size, "", limits, func(it item, _ func() (io.ReadCloser, error)) error {
		total = saturatingAdd(total, it.Size)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}
