package archive

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func FuzzCleanName(f *testing.F) {
	for _, s := range []string{"a/b.txt", "../x", `..\x`, "/abs", "a/./b", "a//b", "a\x00b", "CON", "a/NUL.txt", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		got, err := cleanName(name, Options{})
		if err != nil {
			if !errors.Is(err, ErrTraversal) && !errors.Is(err, ErrUnsafeName) {
				t.Fatalf("untyped error for %q: %v", name, err)
			}
			return
		}
		if strings.HasPrefix(got, "/") || strings.Contains(got, `\`) || strings.ContainsRune(got, 0) {
			t.Fatalf("unsafe result %q from %q", got, name)
		}
		for s := range strings.SplitSeq(got, "/") {
			if s == ".." {
				t.Fatalf("escaping result %q from %q", got, name)
			}
		}
	})
}

// checkExtracted fails when anything was written outside dest (its sibling sentinel folder must stay empty and the
// parent must hold only the two), when dest holds a link or a special file, or when a name is one Windows would alter.
func checkExtracted(t *testing.T, parent, dest string) {
	t.Helper()
	top, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 2 {
		t.Fatalf("extraction wrote beside its destination: %v", top)
	}
	if side, _ := os.ReadDir(filepath.Join(parent, "sentinel")); len(side) != 0 {
		t.Fatalf("extraction wrote into a sibling folder: %v", side)
	}
	seen := map[string]string{}
	err = filepath.WalkDir(dest, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dest {
			return err
		}
		if !d.Type().IsRegular() && !d.IsDir() {
			t.Fatalf("extracted a %v at %s", d.Type(), p)
		}
		if !WindowsSegment(d.Name()) {
			t.Fatalf("extracted a name Windows would alter: %q", p)
		}
		rel, _ := filepath.Rel(dest, p)
		if prev, ok := seen[strings.ToLower(rel)]; ok {
			t.Fatalf("%q and %q collide on a case-insensitive disk", prev, rel)
		}
		seen[strings.ToLower(rel)] = rel
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func extractFuzzed(t *testing.T, archive []byte) {
	t.Helper()
	parent := t.TempDir()
	dest, src := filepath.Join(parent, "dest"), filepath.Join(t.TempDir(), "in")
	for _, d := range []string{dest, filepath.Join(parent, "sentinel")} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(src, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	// Small caps keep a crafted bomb from filling the disk; extraction may fail, but never outside dest.
	// Listing and sizing read the same untrusted headers; they must fail, not panic.
	listFuzzed(t, src)
	sizeFuzzed(t, src)
	extractPath(t, src, dest)
	checkExtracted(t, parent, dest)
}

// extractPath extracts src into the existing dest under the fuzz caps. A refusal
// is the expected outcome for crafted input, so only a panic or a write outside
// dest is a finding, and checkExtracted is what proves the latter.
func extractPath(t *testing.T, src, dest string) {
	t.Helper()
	f, size := openSized(t, src)
	root, err := os.OpenRoot(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	_ = Extract(f, size, filepath.Base(src), root, Options{
		MaxEntryBytes: 1 << 20,
		MaxTotalBytes: 4 << 20,
		MaxEntries:    200,
	})
}

// listFuzzed drives a Lister to exhaustion, so a crafted header reaches the same
// readers a preview would and must fail rather than panic.
func listFuzzed(t *testing.T, src string) {
	t.Helper()
	f, size := openSized(t, src)
	l, err := NewLister(f, size, filepath.Base(src), ListerOptions{MaxEntryBytes: 1 << 20})
	if err != nil {
		return
	}
	defer func() { _ = l.Close() }()
	for {
		if _, err := l.Next(); err != nil {
			return
		}
	}
}

func sizeFuzzed(t *testing.T, src string) {
	t.Helper()
	f, size := openSized(t, src)
	_, _ = DeclaredSize(f, size)
}

// FuzzExtract feeds raw bytes, seeded with every format's fixtures, through the format detection and readers.
func FuzzExtract(f *testing.F) {
	seeds, _ := filepath.Glob("testdata/*")
	for _, p := range seeds {
		if b, err := os.ReadFile(p); err == nil {
			f.Add(b)
		}
	}
	f.Add(zipOf(f, "../escape.txt", 0, []byte("x")))
	f.Add(zipOf(f, "link", fs.ModeSymlink|0o777, []byte("/etc/passwd")))
	f.Fuzz(func(t *testing.T, archive []byte) { extractFuzzed(t, archive) })
}

// FuzzExtractZipEntries writes well-formed zips whose two entries take fuzzed names and modes, so the name and kind
// checks see far more cases than mutated bytes reach.
func FuzzExtractZipEntries(f *testing.F) {
	for _, s := range []struct {
		a, b string
		mode uint32
	}{
		{"a/b.txt", "A/B.txt", 0},
		{"..\\x", "y", 0},
		{"/abs", "z", 0},
		{"dir/", "dir/f", uint32(fs.ModeDir)},
		{"link", "link/x", uint32(fs.ModeSymlink)},
		{"evil.txt:stream", "CON", 0},
		{"x.", "x", 0},
		{"a/../../b", "c", 0},
		{"pipe", "p", uint32(fs.ModeNamedPipe)},
		{"ｆｕｌｌ／ｗｉｄｔｈ", "\u202eexe.txt", 0},
	} {
		f.Add(s.a, s.b, s.mode, []byte("data"))
	}
	f.Fuzz(func(t *testing.T, a, b string, mode uint32, data []byte) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for i, name := range []string{a, b} {
			h := &zip.FileHeader{Name: name, Method: zip.Store}
			if i == 0 {
				h.SetMode(fs.FileMode(mode))
			}
			w, err := zw.CreateHeader(h)
			if err != nil {
				return
			}
			_, _ = w.Write(data)
		}
		if zw.Close() != nil {
			return
		}
		extractFuzzed(t, buf.Bytes())
	})
}

func zipOf(f *testing.F, name string, mode fs.FileMode, data []byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	h := &zip.FileHeader{Name: name, Method: zip.Store}
	h.SetMode(mode)
	w, err := zw.CreateHeader(h)
	if err != nil {
		f.Fatal(err)
	}
	_, _ = w.Write(data)
	if err := zw.Close(); err != nil {
		f.Fatal(err)
	}
	return buf.Bytes()
}

// FuzzExtractStreams seeds the detection and every stream reader with each compressed format, around a tar and bare,
// so mutations reach the decoders and the tar header parser.
func FuzzExtractStreams(f *testing.F) {
	seeds, _ := filepath.Glob("testdata/*.tar*")
	bare, _ := filepath.Glob("testdata/bare.*")
	for _, p := range append(seeds, bare...) {
		if b, err := os.ReadFile(p); err == nil {
			f.Add(b)
		}
	}
	f.Fuzz(func(t *testing.T, archive []byte) { extractFuzzed(t, archive) })
}

// FuzzList asserts the Lister's own contract, which FuzzExtract cannot reach: every
// name arrives normalised, the walk ends in io.EOF, and the two iteration forms
// agree on both the entry count and the size total.
func FuzzList(f *testing.F) {
	seeds, _ := filepath.Glob("testdata/*")
	for _, p := range seeds {
		if b, err := os.ReadFile(p); err == nil {
			f.Add(b)
		}
	}
	f.Add(zipOf(f, `dir\`, 0, []byte("x")))
	f.Add(zipOf(f, "dir/", fs.ModeDir|0o755, nil))
	f.Fuzz(func(t *testing.T, archive []byte) {
		src := writeTemp(t, "listed", archive)
		nextTotal, nextCount, nextErr := listByNext(t, src)
		rangeTotal, rangeCount, rangeErr := listByEntries(t, src)
		if (nextErr == nil) != (rangeErr == nil) {
			t.Fatalf("Next: %v, Entries: %v", nextErr, rangeErr)
		}
		if nextErr != nil {
			return
		}
		if nextCount != rangeCount {
			t.Fatalf("Next saw %d entries, Entries saw %d", nextCount, rangeCount)
		}
		if nextTotal != rangeTotal {
			t.Fatalf("Next totalled %d bytes, Entries totalled %d", nextTotal, rangeTotal)
		}
	})
}

// listByNext walks through Next and Open the way Entries does: header sizes, and
// a drain only for the bare stream that declares none.
func listByNext(t *testing.T, src string) (int64, int, error) {
	t.Helper()
	l, err := openLister(t, src)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = l.Close() }()
	var total int64
	var count int
	for {
		e, err := l.Next()
		if errors.Is(err, io.EOF) {
			return total, count, nil
		}
		if err != nil {
			return total, count, err
		}
		if !normalised(e.Name) {
			t.Fatalf("Next reported %q", e.Name)
		}
		count++
		if e.Size >= 0 {
			total += e.Size
			continue
		}
		rc, err := l.Open()
		if err != nil {
			return total, count, err
		}
		n, err := io.Copy(io.Discard, rc)
		_ = rc.Close()
		if err != nil {
			return total, count, err
		}
		total += n
	}
}

// listByEntries walks the same archive through range-over-func.
func listByEntries(t *testing.T, src string) (int64, int, error) {
	t.Helper()
	l, err := openLister(t, src)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = l.Close() }()
	var total int64
	var count int
	for e, err := range l.Entries() {
		if err != nil {
			return total, count, err
		}
		if !normalised(e.Name) {
			t.Fatalf("Entries reported %q", e.Name)
		}
		count++
		total += e.Size
	}
	return total, count, nil
}

func openLister(t *testing.T, src string) (*Lister, error) {
	t.Helper()
	f, size := openSized(t, src)
	return NewLister(f, size, filepath.Base(src), ListerOptions{MaxEntryBytes: 1 << 20})
}

// normalised reports whether name carries neither a backslash nor a trailing
// slash, the shape Lister promises.
func normalised(name string) bool {
	return !strings.Contains(name, `\`) && !strings.HasSuffix(name, "/")
}
