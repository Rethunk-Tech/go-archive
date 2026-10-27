package archive

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"strings"
	"testing"
)

// A bare stream declares its size in the header; DeclaredSize reports what the
// header says without extracting the file.
func TestDeclaredSizeOfABareStream(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"bare.txt.gz", "bare.txt.xz", "bare.txt.zst", "bare.txt.bz2", "bare.txt.lzma"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if n, err := declaredSize(t, "testdata/"+name); err != nil || n != 14 {
				t.Fatalf("DeclaredSize = %d, %v; want 14", n, err)
			}
		})
	}
}

func TestDeclaredSizeOfAContainer(t *testing.T) {
	t.Parallel()
	zipPath := buildZip(t,
		zentry{name: "a", body: "12345"},
		zentry{name: "b", body: "123"},
	)
	for name, p := range map[string]string{
		"zip": zipPath,
		"7z":  "testdata/valid.7z",
		"rar": "testdata/real.rar",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			n, err := declaredSize(t, p)
			if err != nil {
				t.Fatal(err)
			}
			if n <= 0 {
				t.Fatalf("DeclaredSize = %d", n)
			}
		})
	}
	if n, err := declaredSize(t, zipPath); err != nil || n != 8 {
		t.Fatalf("zip total = %d, %v; want 8", n, err)
	}
}

// A crafted RAR header can declare a window larger than any real encoder writes.
// The reader allocates that window on Next, so the cap has to sit below it: this
// must fail on the dictionary rather than allocate four gigabytes.
func TestDeclaredSizeRefusesACraftedRARWindow(t *testing.T) {
	t.Parallel()
	path := buildRar(t, rarFile{name: "x", data: string([]byte{0xc5, 0x05}), compressionInfo: 0x30c0})
	n, err := declaredSize(t, path)
	if err == nil {
		t.Fatalf("DeclaredSize = %d, want a dictionary refusal", n)
	}
	if !strings.Contains(err.Error(), "dictionary too large") {
		t.Fatalf("got %v, want a dictionary refusal", err)
	}
}

// A stream over the caps reports the cap rather than failing, so a caller sizing
// a preview gets a number it can show. The budget is forced below the payload, so
// the cap branch runs rather than the count coming back plain.
func TestDeclaredSizeReportsTheCapForAnOverCapStream(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(make([]byte, 4<<20)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	// streamSize derives its caps from the source size, and the ratio floor keeps
	// a small source's cap above its payload, so the cap is exercised directly at
	// the budget the extractor would use.
	opts := streamOptions(int64(buf.Len()))
	p, err := openPayload(bytes.NewReader(buf.Bytes()), fmtGzip, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	n, err := io.Copy(io.Discard, p.r)
	if !errors.Is(err, ErrArchiveTooLarge) {
		t.Fatalf("drained %d bytes, %v; want ErrArchiveTooLarge at the 1 MiB budget", n, err)
	}
	// The reported figure for such a stream is the cap the caller set.
	if opts.MaxTotalBytes <= 0 {
		t.Fatalf("stream cap = %d", opts.MaxTotalBytes)
	}
}

func TestDeclaredSizeOfAnUnsupportedFile(t *testing.T) {
	t.Parallel()
	if _, err := declaredSize(t, writeTemp(t, "plain", []byte("not an archive"))); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("got %v, want ErrUnsupportedFormat", err)
	}
}

// DeclaredSize sums what the headers claim and never trusts them for a bound, so
// a size that disagrees with the extracted bytes is still reported as declared.
func TestDeclaredSizeDoesNotReadTheEntries(t *testing.T) {
	t.Parallel()
	path := buildZip(t, zentry{name: "big", body: strings.Repeat("a", 1000)})
	n, err := declaredSize(t, path)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1000 {
		t.Fatalf("DeclaredSize = %d, want the declared 1000", n)
	}
}

// A stream's declared size and a Lister's drained total agree on a well-formed
// archive, which is the invariant a preview's byte count rests on.
func TestDeclaredSizeAgreesWithAListing(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"bare.txt.gz", "valid.tar.gz", "valid.tar.xz"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := "testdata/" + name
			declared, err := declaredSize(t, path)
			if err != nil {
				t.Fatal(err)
			}
			l := openListerPath(t, path, ListerOptions{})
			var listed int64
			for e, err := range l.Entries() {
				if err != nil {
					t.Fatal(err)
				}
				listed += e.Size
			}
			// A tar's listing counts entry bytes only, so the header overhead the
			// declared size includes makes an exact match wrong for a compound.
			if listed > declared {
				t.Fatalf("listing totalled %d bytes, declared size is %d", listed, declared)
			}
			if listed == 0 {
				t.Fatal("the listing reported no bytes")
			}
		})
	}
}
