package archive

import (
	"errors"
	"io"
	"iter"
	"strings"
)

// Entry is one archive entry.
type Entry struct {
	Name  string
	Size  int64
	IsDir bool
}

// ListerOptions bounds what a Lister may decompress. A zero field means the default.
type ListerOptions struct {
	// MaxEntryBytes bounds one entry's decompressed bytes. Zero means maxDecoderBytes (256 MiB),
	// which is stricter than an extraction cap: callers that list streams must raise it.
	MaxEntryBytes int64
}

// budget is what the decoder may produce for the whole listing: one entry's
// allowance plus the tar headers around it. Each entry read through Open is
// capped at MaxEntryBytes again, so a bomb fails either way.
func (o ListerOptions) budget() int64 {
	return streamBudget(o.MaxEntryBytes, DefaultMaxEntries)
}

// Lister walks an archive's entries once, forward.
//
// Next reports each entry in order and returns io.EOF at the end. Open gives the
// current entry's bytes, valid only until the next Next; a caller wanting names
// and sizes alone calls Next without Open and the decoder skips that entry's
// bytes. Nothing here counts entries, checks a name against a destination or
// refuses a link: those are extraction's decisions.
type Lister struct {
	src      Source
	size     int64
	baseName string
	opts     ListerOptions

	// steps pulls the shared walk one entry at a time, and stop releases it.
	steps func() (walkStep, error, bool)
	stop  func()

	gen    int
	step   walkStep
	cur    io.ReadCloser
	opened bool
	closed bool
}

// walkStep is one walked entry with the reader for its bytes.
type walkStep struct {
	item item
	open func() (io.ReadCloser, error)
}

// errNoEntry is what Open reports when no entry is current: before the first
// Next, after io.EOF, or a second time for the entry already handed out.
var errNoEntry = errors.New("no current entry")

// NewLister detects the archive's format from its magic bytes and returns a
// Lister positioned before the first entry. It decompresses nothing until Next.
func NewLister(src Source, size int64, baseName string, opts ListerOptions) (*Lister, error) {
	if opts.MaxEntryBytes == 0 {
		opts.MaxEntryBytes = maxDecoderBytes
	}
	if detectSource(src) == fmtNone {
		return nil, &Error{Reason: ErrUnsupportedFormat}
	}
	l := &Lister{src: src, size: size, baseName: baseName, opts: opts}
	l.steps, l.stop = iter.Pull2(func(yield func(walkStep, error) bool) {
		// The walk runs on the iterator's goroutine, where a panic from a
		// format reader would end the process rather than fail one entry, so
		// it is recovered here instead of being left to the caller.
		var err error
		defer func() {
			if r := recover(); r != nil {
				err = malformed(r)
			}
			// A caller that stopped early ended the walk on purpose; only a
			// real failure is worth reporting through the iterator.
			if err != nil && !errors.Is(err, errWalkStop) {
				yield(walkStep{}, err)
			}
		}()
		// A listing has no entry cap: counting is the caller's policy.
		limits := walkLimits{budget: l.opts.budget()}
		err = walk(l.src, l.size, l.baseName, limits, func(it item, open func() (io.ReadCloser, error)) error {
			if !yield(walkStep{item: it, open: open}, nil) {
				return errWalkStop
			}
			return nil
		})
	})
	return l, nil
}

// Next advances to the next entry, normalising its name. It returns io.EOF once
// the archive is exhausted.
func (l *Lister) Next() (Entry, error) {
	if l.closed {
		return Entry{}, io.EOF
	}
	// The previous entry's reader dies with this call, which is what lets a
	// caller skip an entry it never read.
	_ = l.closeCurrent()
	step, err, ok := l.steps()
	if err != nil {
		return Entry{}, err
	}
	if !ok {
		// Dropping the last entry leaves nothing Open may hand back, so a call
		// after io.EOF reports no current entry rather than repeating it.
		l.step = walkStep{}
		return Entry{}, io.EOF
	}
	l.step, l.gen = step, l.gen+1
	return normalise(step.item.Entry), nil
}

// Open returns the current entry's bytes, valid until the next Next. A
// directory holds no bytes, so it reads empty rather than failing.
func (l *Lister) Open() (io.ReadCloser, error) {
	if l.closed || l.step.open == nil || l.opened {
		return nil, &Error{Entry: l.step.item.Name, Reason: errNoEntry}
	}
	l.opened = true
	if l.step.item.IsDir {
		return io.NopCloser(strings.NewReader("")), nil
	}
	rc, err := l.step.open()
	if err != nil {
		return nil, wrap(l.step.item.Name, err)
	}
	// The per-entry bound belongs here, on the reader handed out, so an entry
	// that overruns it fails rather than decompressing unbounded.
	l.cur = &listReader{
		rc:     &limitReader{r: rc, left: l.opts.MaxEntryBytes, reason: ErrEntryTooLarge},
		lister: l,
		gen:    l.gen,
		name:   l.step.item.Name,
	}
	return l.cur, nil
}

// Close releases the decoders the walk holds. It never closes the caller's
// Source, and calling it twice does nothing.
func (l *Lister) Close() error {
	if l.closed {
		return nil
	}
	l.closed = true
	err := l.closeCurrent()
	if l.stop != nil {
		l.stop()
	}
	return err
}

// closeCurrent releases the reader held for the entry being left behind. A
// caller that never opened the entry, or read only part of one, costs nothing
// beyond the decoder the walk itself holds.
func (l *Lister) closeCurrent() error {
	l.opened = false
	if l.cur == nil {
		return nil
	}
	rc := l.cur
	l.cur = nil
	return rc.Close()
}

// Entries adapts the same walk to range-over-func, draining each entry so the
// sizes it reports are the bytes actually there. Next without Open cannot do
// that, which is why this form exists.
func (l *Lister) Entries() iter.Seq2[Entry, error] {
	return func(yield func(Entry, error) bool) {
		for {
			e, err := l.Next()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				yield(Entry{}, err)
				return
			}
			if !e.IsDir {
				n, err := l.drain()
				if err != nil {
					yield(Entry{}, err)
					return
				}
				e.Size = n
			}
			if !yield(e, nil) {
				return
			}
		}
	}
}

// drain reads the current entry to its end and reports how many bytes it held.
func (l *Lister) drain() (int64, error) {
	rc, err := l.Open()
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(io.Discard, rc)
	if err != nil {
		return n, err
	}
	return n, nil
}

// normalise is an entry's name with backslashes as separators and no trailing
// one, so a folder written as "Mod/" and as "Mod" both list as "Mod".
func normalise(e Entry) Entry {
	e.Name = strings.TrimSuffix(strings.ReplaceAll(e.Name, `\`, "/"), "/")
	return e
}

// listReader is one entry's bytes. It refuses reads once the Lister has moved
// to the next entry, so a zip or 7z reader cannot quietly hand back the
// previous entry's bytes after Next.
type listReader struct {
	rc     io.ReadCloser
	lister *Lister
	gen    int
	name   string
}

func (r *listReader) Read(p []byte) (int, error) {
	if r.lister.gen != r.gen {
		return 0, &Error{Entry: r.name, Reason: errNoEntry}
	}
	return r.rc.Read(p)
}

func (r *listReader) Close() error { return r.rc.Close() }
