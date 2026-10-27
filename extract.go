package archive

import (
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nwaples/rardecode/v2"
)

type extractor struct {
	root    *os.Root
	opts    Options
	entries int
	// ratioMax is the most the archive may unpack to for its size.
	ratioMax int64
	total    int64
	seen     map[string]string // lower-cased path -> spelling first used
	files    map[string]bool
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
	// A writer that separates with backslashes ends a folder's name with one too,
	// which the reader does not see as a folder.
	isDir := it.IsDir || strings.HasSuffix(it.Name, `\`)
	rel, err := x.admit(it.Name, isDir)
	if err != nil {
		return err
	}
	if rel == "" {
		return nil
	}
	return x.entry(it, rel, isDir, open)
}

// entry creates the directory or writes the file, at the path admit returned.
func (x *extractor) entry(it item, rel string, isDir bool, open func() (io.ReadCloser, error)) error {
	target := filepath.FromSlash(rel)
	if isDir {
		if err := x.root.MkdirAll(target, 0o750); err != nil {
			return wrap(it.Name, err)
		}
		return nil
	}
	if dir := filepath.Dir(target); dir != "." {
		if err := x.root.MkdirAll(dir, 0o750); err != nil {
			return wrap(it.Name, err)
		}
	}
	src, err := open()
	if err != nil {
		return wrap(it.Name, err)
	}
	defer func() { _ = src.Close() }()
	return x.write(it.Name, target, src, it.crc)
}

func (x *extractor) write(name, target string, src io.Reader, wantCRC *uint32) error {
	// O_EXCL refuses to write through an existing symlink or over a duplicate.
	out, err := x.root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return wrap(name, err)
	}
	limit := min(x.opts.MaxEntryBytes, x.opts.MaxTotalBytes-x.total, x.ratioMax-x.total)
	h := crc32.NewIEEE()
	// One byte past the limit proves the entry is over it without trusting declared sizes.
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(src, limit+1))
	x.total += n
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return wrap(name, err)
	}
	if n > limit {
		switch {
		case n > x.opts.MaxEntryBytes:
			return &Error{Entry: name, Reason: ErrEntryTooLarge}
		case x.total > x.opts.MaxTotalBytes:
			return &Error{Entry: name, Reason: ErrArchiveTooLarge}
		}
		return &Error{Entry: name, Reason: ErrRatio}
	}
	if wantCRC != nil && h.Sum32() != *wantCRC {
		return &Error{Entry: name, Reason: ErrChecksum}
	}
	return nil
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
	segs := strings.Split(rel, "/")
	for i := range segs {
		p := strings.Join(segs[:i+1], "/")
		if prev, ok := x.seen[strings.ToLower(p)]; ok && prev != p {
			return "", &Error{Entry: name, Reason: ErrCaseCollision}
		}
		x.seen[strings.ToLower(p)] = p
	}
	if !isDir {
		if x.files[rel] {
			return "", &Error{Entry: name, Reason: ErrCaseCollision}
		}
		x.files[rel] = true
	}
	return rel, nil
}
