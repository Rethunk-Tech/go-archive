package archive

import (
	"bytes"
	"errors"
	"io"
	"maps"
	"os"
	"slices"
	"testing"
)

// extractFunc runs ExtractFunc over the archive at path with fn.
func extractFunc(t *testing.T, path string, opts Options, fn func(string, bool, io.Reader) error) error {
	t.Helper()
	f, size := openSized(t, path)
	return ExtractFunc(f, size, "a.zip", opts, fn)
}

// collect is a sink that keeps every file's bytes and every directory's name.
func collect(got map[string]string) func(string, bool, io.Reader) error {
	return func(name string, isDir bool, r io.Reader) error {
		if isDir {
			got[name+"/"] = ""
			return nil
		}
		b, err := io.ReadAll(r)
		got[name] = string(b)
		return err
	}
}

func TestExtractFuncSeesCleanedNames(t *testing.T) {
	t.Parallel()
	got := map[string]string{}
	err := extractFunc(t, buildZip(t, zentry{name: `d\./a.txt`, body: "a"}, zentry{name: "e/"}), Options{}, collect(got))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["d/a.txt"] != "a" || got["e/"] != "" {
		t.Fatalf("got %v", got)
	}
}

func TestExtractFuncReturnsTheCallbacksErrorAsItIs(t *testing.T) {
	t.Parallel()
	mine := errors.New("mine")
	err := extractFunc(t, buildZip(t, zentry{name: "a", body: "a"}), Options{}, func(string, bool, io.Reader) error {
		return mine
	})
	// Unwrap is nil only for the bare error, so nothing wrapped it.
	if !errors.Is(err, mine) || errors.Unwrap(err) != nil {
		t.Fatalf("got %v, want the callback's own error", err)
	}
}

// The bytes the callback reads are what the cap counts, and an over-cap entry
// fails typed even when the callback swallows the read error.
func TestExtractFuncCountsWhatTheCallbackReads(t *testing.T) {
	t.Parallel()
	zip := buildZip(t, zentry{name: "big", body: "0123456789"}, zentry{name: "small", body: "x"})
	err := extractFunc(t, zip, Options{MaxEntryBytes: 4}, func(string, bool, io.Reader) error { return nil })
	if err != nil {
		t.Fatalf("an unread entry was counted: %v", err)
	}
	err = extractFunc(t, zip, Options{MaxEntryBytes: 4}, func(_ string, _ bool, r io.Reader) error {
		_, _ = io.ReadAll(r)
		return nil
	})
	wantReason(t, err, ErrEntryTooLarge, "big")
}

func TestExtractFuncChecksTheCRCOnlyAfterAFullRead(t *testing.T) {
	t.Parallel()
	zb, err := os.ReadFile(buildZip(t, zentry{name: "f", body: "payload"}))
	if err != nil {
		t.Fatal(err)
	}
	zb[bytes.Index(zb, []byte("payload"))] ^= 0xff
	bad := writeTemp(t, "bad.zip", zb)
	err = extractFunc(t, bad, Options{}, func(_ string, _ bool, r io.Reader) error {
		_, err := r.Read(make([]byte, 3))
		return err
	})
	if err != nil {
		t.Fatalf("a partial read was checksummed: %v", err)
	}
	err = extractFunc(t, bad, Options{}, collect(map[string]string{}))
	wantReason(t, err, ErrChecksum, "f")
}

func TestExtractFuncHonoursSegment(t *testing.T) {
	t.Parallel()
	zip := buildZip(t, zentry{name: "CON.txt", body: "x"})
	err := extractFunc(t, zip, Options{}, collect(map[string]string{}))
	wantReason(t, err, ErrUnsafeName, "CON.txt")
	got := map[string]string{}
	if err := extractFunc(t, zip, Options{Segment: func(string) bool { return true }}, collect(got)); err != nil {
		t.Fatal(err)
	}
	if got["CON.txt"] != "x" {
		t.Fatalf("got %v", got)
	}
}

func TestExtractFuncCaseSensitive(t *testing.T) {
	t.Parallel()
	variants := buildZip(t, zentry{name: "a/X/1", body: "1"}, zentry{name: "a/x/2", body: "2"})
	got := map[string]string{}
	if err := extractFunc(t, variants, Options{CaseSensitive: true}, collect(got)); err != nil {
		t.Fatal(err)
	}
	if names := slices.Sorted(maps.Keys(got)); !slices.Equal(names, []string{"a/X/1", "a/x/2"}) {
		t.Fatalf("got %v", names)
	}
	err := extractFunc(t, variants, Options{}, collect(map[string]string{}))
	wantReason(t, err, ErrCaseCollision, "a/x/2")

	twice := buildZip(t, zentry{name: "a/b", body: "1"}, zentry{name: "a/b", body: "2"})
	err = extractFunc(t, twice, Options{CaseSensitive: true}, collect(map[string]string{}))
	wantReason(t, err, ErrCaseCollision, "a/b")
}
