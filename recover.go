package archive

import (
	"fmt"
	"io"

	"github.com/bodgit/sevenzip"
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
	return sevenzip.NewReader(r, size)
}
