package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// openZipLister writes entries as a zip and returns a Lister over it.
func openZipLister(t *testing.T, entries ...zentry) *Lister {
	t.Helper()
	return openListerPath(t, buildZip(t, entries...), ListerOptions{})
}

func openListerPath(t *testing.T, path string, opts ListerOptions) *Lister {
	t.Helper()
	l, err := openListerOpts(t, path, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func openListerOpts(t *testing.T, path string, opts ListerOptions) (*Lister, error) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	return NewLister(f, info.Size(), filepath.Base(path), opts)
}

// names drains the Lister and returns each entry it reported.
func names(t *testing.T, l *Lister) []Entry {
	t.Helper()
	var out []Entry
	for {
		e, err := l.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
}

// The one normalisation a listing performs is spelling: a backslash becomes a
// separator and a trailing one is dropped, so "Mod/" and "Mod" list alike.
func TestListerNormalisesNames(t *testing.T) {
	t.Parallel()
	l := openZipLister(t,
		zentry{name: "Mod/", mode: os.ModeDir | 0o755},
		zentry{name: "Mod/manifest.json", body: "{}"},
		zentry{name: `Mod\sub\`, mode: os.ModeDir | 0o755},
		zentry{name: `Mod\sub\a.txt`, body: "a"},
	)
	got := names(t, l)
	want := []Entry{
		{Name: "Mod", IsDir: true},
		{Name: "Mod/manifest.json"},
		{Name: "Mod/sub", IsDir: true},
		{Name: "Mod/sub/a.txt"},
	}
	if len(got) != len(want) {
		t.Fatalf("listed %d entries, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Name != want[i].Name || got[i].IsDir != want[i].IsDir {
			t.Fatalf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestListerEndsInEOF(t *testing.T) {
	t.Parallel()
	l := openZipLister(t, zentry{name: "only.txt", body: "x"})
	if _, err := l.Next(); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("second Next = %v, want io.EOF", err)
	}
	// The end is stable: a caller that loops past it must not spin or panic.
	if _, err := l.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("third Next = %v, want io.EOF", err)
	}
}

// Skipping an entry's bytes is what makes listing a large archive cheap, so the
// walk must advance the decoder past whatever the caller left unread.
func TestListerSkipsAnUnreadEntry(t *testing.T) {
	t.Parallel()
	l := openZipLister(t,
		zentry{name: "first.txt", body: strings.Repeat("a", 4096)},
		zentry{name: "second.txt", body: "second"},
	)
	if _, err := l.Next(); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Next(); err != nil {
		t.Fatal(err)
	}
	rc, err := l.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second" {
		t.Fatalf("skipping the first entry lost alignment: %q", got)
	}
}

// Open is valid only until the next Next, a hard constraint for the forward-only
// formats that is stated for all of them so no caller depends on the difference.
func TestListerReadingThePreviousEntryFails(t *testing.T) {
	t.Parallel()
	l := openZipLister(t,
		zentry{name: "a.txt", body: "a"},
		zentry{name: "b.txt", body: "b"},
	)
	if _, err := l.Next(); err != nil {
		t.Fatal(err)
	}
	rc, err := l.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	if _, err := l.Next(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(rc); !errors.Is(err, errNoEntry) {
		t.Fatalf("read after Next = %v, want errNoEntry", err)
	}
}

func TestListerOpenRefusesWhenNoEntryIsCurrent(t *testing.T) {
	t.Parallel()
	l := openZipLister(t, zentry{name: "a.txt", body: "a"})
	if _, err := l.Open(); !errors.Is(err, errNoEntry) {
		t.Fatalf("Open before Next = %v, want errNoEntry", err)
	}
	if _, err := l.Next(); err != nil {
		t.Fatal(err)
	}
	rc, err := l.Open()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rc.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	if _, err := l.Open(); !errors.Is(err, errNoEntry) {
		t.Fatalf("second Open = %v, want errNoEntry", err)
	}
	if _, err := l.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after the only entry = %v, want io.EOF", err)
	}
	if _, err := l.Open(); !errors.Is(err, errNoEntry) {
		t.Fatalf("Open after io.EOF = %v, want errNoEntry", err)
	}
}

func TestListerReportsAStreamsBareFile(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"bare.txt.gz", "bare.txt.xz", "bare.txt.zst", "bare.txt.bz2", "bare.txt.lzma"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			l := openListerPath(t, filepath.Join("testdata", name), ListerOptions{})
			got := names(t, l)
			if len(got) != 1 || got[0].Name != "bare.txt" || got[0].IsDir {
				t.Fatalf("listed %+v", got)
			}
		})
	}
}

// A bare stream's header declares no size, so the count is the bytes drained,
// which is what Entries exists to report.
func TestListerEntriesTotalsTheBytesDrained(t *testing.T) {
	t.Parallel()
	l := openListerPath(t, filepath.Join("testdata", "valid.tar.gz"), ListerOptions{})
	var total int64
	for e, err := range l.Entries() {
		if err != nil {
			t.Fatal(err)
		}
		total += e.Size
	}
	if total == 0 {
		t.Fatal("Entries reported no bytes")
	}
}

// The bound is on the reader Open hands out, so a bomb fails there.
func TestListerMaxEntryBytesStopsAStreamBomb(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(make([]byte, 8<<20)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	src := writeTemp(t, "bomb.gz", buf.Bytes())
	l := openListerPath(t, src, ListerOptions{MaxEntryBytes: 1 << 20})
	if _, err := l.Next(); err != nil {
		t.Fatal(err)
	}
	rc, err := l.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	n, err := io.Copy(io.Discard, rc)
	if !errors.Is(err, ErrEntryTooLarge) {
		t.Fatalf("copy = %d bytes, %v; want ErrEntryTooLarge", n, err)
	}
	if n > 1<<20 {
		t.Fatalf("read %d bytes past a 1 MiB bound", n)
	}
}

// A listing has no entry cap, so a large archive lists to the end rather than
// being refused for counting.
func TestListerHasNoEntryCap(t *testing.T) {
	t.Parallel()
	entries := make([]zentry, 0, 64)
	for i := range 64 {
		entries = append(entries, zentry{name: "f" + strconv.Itoa(i) + ".txt", body: "x"})
	}
	l := openZipLister(t, entries...)
	if got := len(names(t, l)); got != 64 {
		t.Fatalf("listed %d entries, want 64", got)
	}
}

// The per-entry bound is what a caller sets, so an archive of many legitimate
// entries inside that bound lists to the end. A whole-listing budget of one
// entry's allowance would stop such an archive partway.
func TestListerListsManyEntriesInsideThePerEntryBound(t *testing.T) {
	t.Parallel()
	// Together these are an order of magnitude past the 1 MiB per-entry bound,
	// while no single entry comes near it, so only an aggregate budget sized from
	// the per-entry allowance can refuse the archive.
	const entries, size = 300, 256 << 10
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	body := bytes.Repeat([]byte("a"), size)
	for i := range entries {
		h := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     "d/" + strconv.Itoa(i) + ".bin",
			Mode:     0o644,
			Size:     size,
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	l := openListerPath(t, writeTemp(t, "many.tar", buf.Bytes()), ListerOptions{MaxEntryBytes: 1 << 20})
	if got := names(t, l); len(got) != entries {
		t.Fatalf("listed %d of %d entries", len(got), entries)
	}
}

func TestListerUnsupportedFormat(t *testing.T) {
	t.Parallel()
	_, err := openListerOpts(t, writeTemp(t, "plain", []byte("not an archive")), ListerOptions{})
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("got %v, want ErrUnsupportedFormat", err)
	}
}

// Close is idempotent and never closes the caller's Source.
func TestListerCloseIsIdempotentAndLeavesTheSourceOpen(t *testing.T) {
	t.Parallel()
	path := buildZip(t, zentry{name: "a.txt", body: "a"})
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	l, err := NewLister(f, info.Size(), "a.zip", ListerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Next(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := f.Stat(); err != nil {
		t.Fatalf("Close closed the caller's Source: %v", err)
	}
	if _, err := l.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after Close = %v, want io.EOF", err)
	}
}

// Encryption is extraction's policy, so a listing reports the entry. The zip
// walk never reads the encrypted flag.
func TestListerReportsAnEncryptedEntryThatExtractRefuses(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "secret.txt", Method: zip.Store, Flags: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := writeTemp(t, "enc.zip", buf.Bytes())
	got := names(t, openListerPath(t, path, ListerOptions{}))
	if len(got) != 1 || got[0].Name != "secret.txt" {
		t.Fatalf("listed %+v, want the encrypted entry named", got)
	}
	if _, err := extractDir(t, path, Options{}); !errors.Is(err, ErrEncrypted) {
		t.Fatalf("Extract = %v, want ErrEncrypted", err)
	}
}

// A link lists as an ordinary file; only Extract distinguishes the kind.
func TestListerReportsALinkAsAFile(t *testing.T) {
	t.Parallel()
	var found bool
	for _, e := range names(t, openListerPath(t, "testdata/symlink.7z", ListerOptions{})) {
		if e.Name == "link" {
			found = true
			if e.IsDir {
				t.Fatalf("link listed as a directory: %+v", e)
			}
		}
	}
	if !found {
		t.Fatal("the symlink entry was not listed")
	}
}

// A crafted header that declares an impossible window must fail on the dictionary
// cap rather than allocate it. The reader allocates the window when it decodes the
// block header, so the refusal arrives from Next rather than from NewLister.
func TestListerRefusesACraftedRARDictionary(t *testing.T) {
	t.Parallel()
	path := buildRar(t, rarFile{name: "x", data: string([]byte{0xc5, 0x05}), compressionInfo: 0x30c0})
	l := openListerPath(t, path, ListerOptions{})
	_, err := l.Next()
	if err == nil || !strings.Contains(err.Error(), "dictionary too large") {
		t.Fatalf("Next = %v, want a dictionary refusal", err)
	}
}

func TestListerMaxEntryBytesDefaultsToTheDecoderCap(t *testing.T) {
	t.Parallel()
	l, err := openListerOpts(t, buildZip(t, zentry{name: "a.txt", body: "a"}), ListerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if l.opts.MaxEntryBytes != maxDecoderBytes {
		t.Fatalf("default bound = %d, want maxDecoderBytes", l.opts.MaxEntryBytes)
	}
}

// writePaxTar builds a tar carrying a pax global header ahead of one real file.
func writePaxTar(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Typeflag:   tar.TypeXGlobalHeader,
		Format:     tar.FormatPAX,
		PAXRecords: map[string]string{"mtime": "1.0"},
	}); err != nil {
		t.Fatal(err)
	}
	content := []byte("data")
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "real/file.txt", Mode: 0o644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return writeTemp(t, "pax.tar", buf.Bytes())
}

// skip marks an entry naming no file of its own, so a tar's pax global header
// never reaches a caller as an entry.
func TestListerSkipsAPaxGlobalHeader(t *testing.T) {
	t.Parallel()
	path := writePaxTar(t)
	for _, e := range names(t, openListerPath(t, path, ListerOptions{})) {
		if e.Name == "" || strings.HasPrefix(e.Name, "pax") {
			t.Fatalf("listed a header entry: %+v", e)
		}
	}
}
