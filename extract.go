package archive

import (
	"bytes"
	"errors"
	"hash"
	"hash/crc32"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rethunk-Tech/go-archive/internal/rardecode"
)

type extractor struct {
	opts    Options
	entries int
	// ratioMax is the most the archive may unpack to for its size.
	ratioMax int64
	total    int64
	seen     map[string]string // lower-cased path -> spelling first used
	files    map[string]bool
	sink     func(name string, isDir bool, r io.Reader) error
	// wrapSink types the sink's errors, for the root writer whose errors are
	// filesystem errors; a caller's sink keeps its own.
	wrapSink bool
}

func newExtractor(opts Options, size int64, sink func(name string, isDir bool, r io.Reader) error) *extractor {
	x := &extractor{opts: opts.withDefaults(), seen: map[string]string{}, files: map[string]bool{}, sink: sink}
	x.ratioMax = x.opts.ratioCap(size)
	return x
}

func checkKind(name string, mode fs.FileMode) error {
	switch {
	case mode&fs.ModeSymlink != 0:
		return &Error{Entry: name, Reason: ErrLink}
	case mode&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket|fs.ModeIrregular) != 0:
		return &Error{Entry: name, Reason: ErrSpecialFile}
	}
	return nil
}

// admitEntry is extraction's policy for one walked entry: refuse encryption,
// links and special files, then admit the name before anything reaches the
// destination.
func (x *extractor) admitEntry(it item, open func() (io.ReadCloser, error)) error {
	if it.encrypted {
		return &Error{Entry: it.Name, Reason: ErrEncrypted}
	}
	if it.linkType != int(rardecode.LinkTypeNone) {
		return &Error{Entry: it.Name, Reason: ErrLink}
	}
	if err := checkKind(it.Name, it.mode); err != nil {
		return err
	}
	rel, err := x.admit(it.Name, it.IsDir)
	if err != nil {
		return err
	}
	if rel == "" {
		return nil
	}
	return x.entry(it, rel, it.IsDir, open)
}

// entry hands the directory or file at the path admit returned to the sink,
// counting the bytes the sink reads against the caps.
func (x *extractor) entry(it item, rel string, isDir bool, open func() (io.ReadCloser, error)) error {
	if isDir {
		return x.sinkErr(it.Name, x.sink(rel, true, bytes.NewReader(nil)))
	}
	src, err := open()
	if err != nil {
		return wrap(it.Name, err)
	}
	defer func() { _ = src.Close() }()
	// The ceiling below MaxInt64 keeps limit+1 from wrapping when every cap is MaxInt64.
	limit := min(x.opts.MaxEntryBytes, x.opts.MaxTotalBytes-x.total, x.ratioMax-x.total, math.MaxInt64-1)
	// One byte past the limit proves the entry is over it without trusting declared sizes.
	r := &entryReader{r: io.LimitReader(src, limit+1), limit: limit, h: crc32.NewIEEE()}
	err = x.sink(rel, false, r)
	x.total += r.n
	// The cap and the source's own failure win over whatever the sink made of
	// them, so a sink that swallows a read error cannot pass an over-cap entry.
	if r.n > limit {
		switch {
		case r.n > x.opts.MaxEntryBytes:
			return &Error{Entry: it.Name, Reason: ErrEntryTooLarge}
		case x.total > x.opts.MaxTotalBytes:
			return &Error{Entry: it.Name, Reason: ErrArchiveTooLarge}
		}
		return &Error{Entry: it.Name, Reason: ErrRatio}
	}
	if r.err != nil {
		return wrap(it.Name, r.err)
	}
	if err != nil {
		return x.sinkErr(it.Name, err)
	}
	if r.eof && it.crc != nil && r.h.Sum32() != *it.crc {
		return &Error{Entry: it.Name, Reason: ErrChecksum}
	}
	return nil
}

func (x *extractor) sinkErr(name string, err error) error {
	if err == nil || !x.wrapSink {
		return err
	}
	return wrap(name, err)
}

// errOverCap is what an entry's reader returns past its cap; entry replaces it
// with the typed reason once the sink returns.
var errOverCap = errors.New("entry exceeds its cap")

// entryReader counts and checksums what a sink reads, and stops it one byte
// past the cap.
type entryReader struct {
	r     io.Reader
	limit int64
	n     int64
	h     hash.Hash32
	err   error // the source's failure, other than EOF
	eof   bool
}

func (e *entryReader) Read(p []byte) (int, error) {
	if e.n > e.limit {
		return 0, errOverCap
	}
	if e.err != nil {
		return 0, e.err
	}
	n, err := e.r.Read(p)
	e.n += int64(n)
	_, _ = e.h.Write(p[:n])
	if e.n > e.limit {
		return n, errOverCap
	}
	switch {
	case errors.Is(err, io.EOF):
		e.eof = true
	case err != nil:
		e.err = err
	}
	return n, err
}

// rootSink writes each entry under root: directories at 0o750, files created
// exclusively at 0o600.
func rootSink(root *os.Root) func(name string, isDir bool, r io.Reader) error {
	return func(name string, isDir bool, r io.Reader) error {
		target := filepath.FromSlash(name)
		if isDir {
			return root.MkdirAll(target, 0o750)
		}
		if dir := filepath.Dir(target); dir != "." {
			if err := root.MkdirAll(dir, 0o750); err != nil {
				return err
			}
		}
		// O_EXCL refuses to write through an existing symlink or over a duplicate.
		out, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, r)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		return err
	}
}

// admit counts the entry and returns its clean slash-separated path, or ""
// for an entry naming the destination itself.
func (x *extractor) admit(name string, isDir bool) (string, error) {
	rel, err := cleanName(name, x.opts)
	if err != nil {
		return "", &Error{Entry: name, Reason: err}
	}
	if rel == "" {
		return "", nil
	}
	x.entries++
	if x.entries > x.opts.MaxEntries {
		return "", &Error{Entry: name, Reason: ErrTooManyEntries}
	}
	if !x.opts.CaseSensitive {
		segs := strings.Split(rel, "/")
		for i := range segs {
			p := strings.Join(segs[:i+1], "/")
			if prev, ok := x.seen[strings.ToLower(p)]; ok && prev != p {
				return "", &Error{Entry: name, Reason: ErrCaseCollision}
			}
			x.seen[strings.ToLower(p)] = p
		}
	}
	if !isDir {
		if x.files[rel] {
			return "", &Error{Entry: name, Reason: ErrCaseCollision}
		}
		x.files[rel] = true
	}
	return rel, nil
}
