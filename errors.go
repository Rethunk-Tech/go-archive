package archive

import (
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"

	"github.com/Rethunk-Tech/go-archive/internal/rardecode"
	"github.com/bodgit/sevenzip"
	"github.com/klauspost/compress/zstd"
)

// Reasons carried by Error. A disk-full write is reported as the underlying
// error, so errors.Is(err, syscall.ENOSPC) holds.
var (
	ErrUnsupportedFormat = errors.New("not a zip, RAR, 7z, tar, gzip, xz, lzma, zstd, bzip2 or lz4 archive")
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
	ErrDecoderMemory     = errors.New("decoder memory exceeds the extraction cap")
)

// Error names the entry that failed and why. Entry is empty for a failure of
// the archive as a whole.
type Error struct {
	Entry  string
	Reason error
}

func (e *Error) Error() string {
	if e.Entry == "" {
		return fmt.Sprintf("archive: %v", e.Reason)
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
	case errors.Is(err, rardecode.ErrSolidOverrun):
		err = fmt.Errorf("%w: %w", ErrMalformed, err)
	case errors.Is(err, rardecode.ErrDictionaryTooLarge), errors.Is(err, zstd.ErrWindowSizeExceeded):
		err = fmt.Errorf("%w: %w", ErrDecoderMemory, err)
	}
	return &Error{Entry: entry, Reason: err}
}
