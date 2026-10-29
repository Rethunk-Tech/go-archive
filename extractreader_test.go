package archive

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// extractReader runs ExtractReader over b, read through a plain io.Reader so
// nothing can seek, into a fresh directory.
func extractReader(t *testing.T, b []byte, baseName string) (string, error) {
	t.Helper()
	dest := t.TempDir()
	root, err := os.OpenRoot(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	return dest, ExtractReader(io.MultiReader(bytes.NewReader(b)), baseName, root, Options{})
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestExtractReaderReadsForwardOnlyFormats(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"valid.tar": "Mod/Fix.dll", "valid.tar.gz": "Mod/Fix.dll", "real.rar": "asd.go", "bare.txt.gz": "bare.txt",
	} {
		dest, err := extractReader(t, fixture(t, name), name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(want))); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestExtractReaderRefusesRandomAccessFormats(t *testing.T) {
	t.Parallel()
	zipped, err := os.ReadFile(buildZip(t, zentry{name: "a", body: "a"}))
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{"a.zip": zipped, "valid.7z": fixture(t, "valid.7z")} {
		_, err := extractReader(t, b, name)
		wantReason(t, err, ErrUnsupportedFormat, "")
	}
}

// failingReader serves its bytes, then fails.
type failingReader struct {
	r   io.Reader
	err error
}

func (f *failingReader) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	if errors.Is(err, io.EOF) {
		return n, f.err
	}
	return n, err
}

func TestExtractReaderReturnsTheReadersError(t *testing.T) {
	t.Parallel()
	dropped := errors.New("connection dropped")
	b := fixture(t, "valid.tar")
	for _, cut := range []int{100, 700} {
		dest := t.TempDir()
		root, err := os.OpenRoot(dest)
		if err != nil {
			t.Fatal(err)
		}
		err = ExtractReader(&failingReader{r: bytes.NewReader(b[:cut]), err: dropped}, "x.tar", root, Options{})
		_ = root.Close()
		if !errors.Is(err, dropped) {
			t.Errorf("cut at %d: got %v, want the reader's error", cut, err)
		}
	}
}

// A body that ends early is a short read whichever way it was cut, never a
// format the package does not read.
func TestExtractReaderShortBodyIsUnexpectedEOF(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"valid.tar", "valid.tar.gz"} {
		_, err := extractReader(t, fixture(t, name)[:100], name)
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("%s: got %v, want io.ErrUnexpectedEOF", name, err)
		}
	}
}

func TestExtractReaderEmptyTar(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := tar.NewWriter(&buf).Close(); err != nil {
		t.Fatal(err)
	}
	dest, err := extractReader(t, buf.Bytes(), "item.tar")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadDir(dest); len(got) != 0 {
		t.Fatalf("extracted %d entries", len(got))
	}
	_, err = extractReader(t, buf.Bytes(), "item")
	wantReason(t, err, ErrUnsupportedFormat, "")
}
