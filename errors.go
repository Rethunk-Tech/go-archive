package archive

import (
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"

	"github.com/bodgit/sevenzip"
	"github.com/klauspost/compress/zstd"
	"github.com/nwaples/rardecode/v2"
)

// Reasons carried by Error. A disk-full write is reported as the underlying
// error, so errors.Is(err, syscall.ENOSPC) holds.
var (
	ErrUnsupportedFormat = errors.New("not a zip, RAR, 7z, tar, gzip, xz, lzma, zstd or bzip2 archive")
	ErrEncrypted         = errors.New("archive is encrypted")
	ErrTraversal         = errors.New("path escapes the destination")
	ErrUnsafeName        = errors.New("name is not allowed")
	ErrCaseCollision     = errors.New("name collides with another entry")
	ErrLink              = errors.New("symlinks and hardlinks are not allowed")
	ErrSpecialFile       = errors.New("device, pipe and socket entries are not allowed")
	ErrEntryTooLarge     = errors.New("entry exceeds the size cap")
	ErrArchiveTooLarge   = errors.New("archive exceeds the extracted size cap")
	ErrTooManyEntries    = errors.New("archive exceeds the entry cap")
	ErrRatio             = errors.New("archive unpacks to far more than its size allows")
	ErrChecksum          = errors.New("checksum mismatch")
	ErrMalformed         = errors.New("archive is malformed")
)

// Error names the entry that failed and why. Entry is empty for a failure of
// the archive as a whole.
type Error struct {
	Entry  string
	Reason error
}

func (e *Error) Error() string {
	if e.Entry == "" {
		return "archive: " + e.Reason.Error()
	}
	return fmt.Sprintf("archive: entry %q: %v", e.Entry, e.Reason)
}

func (e *Error) Unwrap() error { return e.Reason }

// wrap types a library or filesystem error as an *Error. Filesystem errors
// stay in the chain, so errors.Is(err, syscall.ENOSPC) reports a full disk.
func wrap(entry string, err error) error {
	if _, typed := errors.AsType[*Error](err); typed {
		return err
	}
	re, isReadError := errors.AsType[*sevenzip.ReadError](err)
	switch {
	case errors.Is(err, zip.ErrChecksum), errors.Is(err, rardecode.ErrBadFileChecksum), errors.Is(err, gzip.ErrChecksum),
		errors.Is(err, zstd.ErrCRCMismatch):
		err = ErrChecksum
	case isReadError && re.Encrypted:
		err = ErrEncrypted
	}
	return &Error{Entry: entry, Reason: err}
}
