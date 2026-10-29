// Package archive reads untrusted archives and writes what Extract unpacks
// under a root the caller supplies.
//
// Format comes from the magic bytes, never the extension: zip, RAR 4 and 5, 7z,
// tar, and gzip, xz, lzma, zstd or bzip2, each alone or wrapped around a tar.
// Every reader is bounded in memory and in bytes before it produces output.
//
// Extract enforces the name, kind and size policy. Lister walks the same entries
// and only bounds decompression.
package archive

import (
	"io"
	"os"
)

// Default caps, applied to every zero field of Options. The byte caps sit far
// above any real archive, so the ratio guard rather than these stops a small
// archive claiming them.
const (
	DefaultMaxEntryBytes int64 = 32 << 30
	DefaultMaxTotalBytes int64 = 64 << 30
	DefaultMaxEntries          = 20000

	// maxDecoderBytes caps the window or dictionary a decompressor may allocate. It
	// is separate from the entry cap because a bigger file must not mean a bigger
	// in-memory dictionary.
	maxDecoderBytes int64 = 256 << 20

	// Below DefaultRatioFloor an archive may unpack to any size within the caps;
	// above it, to at most DefaultMaxRatio times its own size. Deflate tops out
	// near 1030:1, so real archives stay well inside.
	DefaultMaxRatio   int64 = 1024
	DefaultRatioFloor int64 = 2 << 30
)

// Options lowers or raises the extraction caps; a zero field means its default.
type Options struct {
	MaxEntryBytes int64
	MaxTotalBytes int64
	MaxEntries    int
	MaxRatio      int64
	RatioFloor    int64
	// Segment reports whether one path segment is a name the destination platform
	// will store as written. nil means WindowsSegment.
	Segment func(seg string) bool
}

// segment is the name policy cleanName applies, defaulting to WindowsSegment.
func (o Options) segment() func(seg string) bool {
	if o.Segment == nil {
		return WindowsSegment
	}
	return o.Segment
}

// ratioCap is the most an archive of srcSize bytes may unpack to, or
// MaxTotalBytes when its size is unknown.
func (o Options) ratioCap(srcSize int64) int64 {
	// The product cannot exceed MaxTotalBytes once srcSize does, and computing it
	// past that point could wrap.
	if srcSize <= 0 || o.MaxRatio <= 0 || srcSize > o.MaxTotalBytes/o.MaxRatio {
		return o.MaxTotalBytes
	}
	return min(o.MaxTotalBytes, max(o.RatioFloor, srcSize*o.MaxRatio))
}

// streamLimits bounds what a stream of srcSize bytes may decompress. The ratio
// applies here as well as to what is written, because an entry that is skipped
// rather than written is still decompressed to reach the next one.
func (o Options) streamLimits(srcSize int64) walkLimits {
	limit, over := o.ratioCap(srcSize), ErrArchiveTooLarge
	if limit < o.MaxTotalBytes {
		over = ErrRatio
	}
	return walkLimits{budget: streamBudget(limit, o.MaxEntries), over: over}
}

func (o Options) withDefaults() Options {
	if o.MaxEntryBytes == 0 {
		o.MaxEntryBytes = DefaultMaxEntryBytes
	}
	if o.MaxTotalBytes == 0 {
		o.MaxTotalBytes = DefaultMaxTotalBytes
	}
	if o.MaxEntries == 0 {
		o.MaxEntries = DefaultMaxEntries
	}
	if o.MaxRatio == 0 {
		o.MaxRatio = DefaultMaxRatio
	}
	if o.RatioFloor == 0 {
		o.RatioFloor = DefaultRatioFloor
	}
	return o
}

// Source is what a reader needs. *os.File, *bytes.Reader and *io.SectionReader
// satisfy it. Every read is at an offset, so the caller's file position is
// never moved.
type Source interface {
	io.ReaderAt
}

// Extract unpacks the archive src (size bytes) into dest, detecting the format
// from its magic bytes. dest must exist and is rooted by the caller. baseName
// names the single file a bare compressed stream yields. Extraction stops at
// the first failure and leaves what it wrote in dest, so the caller passes a
// temp directory and discards it on error. A zero Options field means its
// default.
func Extract(src Source, size int64, baseName string, dest *os.Root, opts Options) (err error) {
	defer RecoverMalformed(&err)

	x := &extractor{root: dest, opts: opts.withDefaults(), seen: map[string]string{}, files: map[string]bool{}}
	x.ratioMax = x.opts.ratioCap(size)
	limits := x.opts.streamLimits(size)
	// The declared count is refused up front where a container can report it;
	// admit counts the rest as they are written.
	limits.maxEntries = x.opts.MaxEntries
	return walk(src, size, baseName, limits, x.admitEntry)
}
