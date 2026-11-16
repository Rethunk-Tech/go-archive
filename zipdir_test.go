package archive

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// lyingZip is a zip of n empty central records whose end record claims one record in 46 bytes at offset 30, which
// archive/zip does not hold against the n it reads.
func lyingZip(n int) []byte {
	var out bytes.Buffer
	var lh [30]byte
	binary.LittleEndian.PutUint32(lh[:], 0x04034b50)
	out.Write(lh[:])
	for range n {
		var r [46]byte
		binary.LittleEndian.PutUint32(r[:], zipCDSig)
		binary.LittleEndian.PutUint16(r[4:], 20)
		binary.LittleEndian.PutUint16(r[6:], 20)
		out.Write(r[:])
	}
	var e [22]byte
	binary.LittleEndian.PutUint32(e[:], zipEOCDSig)
	binary.LittleEndian.PutUint16(e[8:], 1)
	binary.LittleEndian.PutUint16(e[10:], 1)
	binary.LittleEndian.PutUint32(e[12:], 46)
	binary.LittleEndian.PutUint32(e[16:], 30)
	out.Write(e[:])
	return out.Bytes()
}

func TestZipEntryCapHoldsWhenTheEndRecordUnderstatesTheDirectory(t *testing.T) {
	t.Parallel()
	raw := lyingZip(1000)
	err := ExtractFunc(bytes.NewReader(raw), int64(len(raw)), "x.zip", Options{MaxEntries: 100},
		func(string, bool, io.Reader) error { return nil })
	if !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("got %v, want ErrTooManyEntries", err)
	}
	// Refused before archive/zip allocates a record: checkZipDirectory alone must answer.
	if err := checkZipDirectory(bytes.NewReader(raw), int64(len(raw)), walkLimits{maxEntries: 100}); !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("precheck got %v", err)
	}
}

func TestZipPrecheckRejectsADecoyRecord(t *testing.T) {
	t.Parallel()
	decoy := make([]byte, zipMinRecord)
	binary.LittleEndian.PutUint32(decoy, zipCDSig)
	binary.LittleEndian.PutUint16(decoy[32:], 0xFFFF) // comment runs past the end of the file
	var raw bytes.Buffer
	raw.Write(decoy)
	var records [200 * zipMinRecord]byte
	for i := 0; i < len(records); i += zipMinRecord {
		binary.LittleEndian.PutUint32(records[i:], zipCDSig)
	}
	raw.Write(records[:])
	var e [22]byte
	binary.LittleEndian.PutUint32(e[:], zipEOCDSig)
	binary.LittleEndian.PutUint16(e[8:], 1)
	binary.LittleEndian.PutUint16(e[10:], 1)
	binary.LittleEndian.PutUint32(e[12:], uint32(len(records)))
	binary.LittleEndian.PutUint32(e[16:], 0)
	raw.Write(e[:])
	if err := checkZipDirectory(bytes.NewReader(raw.Bytes()), int64(raw.Len()), walkLimits{maxEntries: 100}); !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("precheck got %v, want ErrTooManyEntries", err)
	}
}

func TestDeclaredSizeHonoursTheEntryCap(t *testing.T) {
	t.Parallel()
	raw := lyingZip(DefaultMaxEntries + 1)
	if _, err := DeclaredSize(bytes.NewReader(raw), int64(len(raw))); !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("got %v, want ErrTooManyEntries", err)
	}
}

func TestCheckZipDirectorySkipsATrailingDecoyEOCD(t *testing.T) {
	t.Parallel()
	raw := lyingZip(1000)
	decoy := make([]byte, zipEOCDSize)
	binary.LittleEndian.PutUint32(decoy, zipEOCDSig)
	binary.LittleEndian.PutUint16(decoy[20:], 0xFFFF) // comment length overruns the tail
	raw = append(raw, decoy...)
	if err := checkZipDirectory(bytes.NewReader(raw), int64(len(raw)), walkLimits{maxEntries: 100}); !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("precheck got %v, want ErrTooManyEntries", err)
	}
}

// paddedZip is a two-entry zip whose central records each carry a 2 KiB extra field and a 1 KiB comment, so its end
// record declares a directory of about 6 KiB while the record count stays at two.
func paddedZip(t *testing.T) []byte {
	t.Helper()
	const pad = 2048
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, name := range []string{"a", "b"} {
		// An extra field of an unassigned header ID: a reader skips it rather than failing on it.
		extra := make([]byte, 4+pad)
		binary.LittleEndian.PutUint16(extra, 0x9999)
		binary.LittleEndian.PutUint16(extra[2:], pad)
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name: name, Method: zip.Store, Extra: extra, Comment: strings.Repeat("c", pad/2),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// The cap is per allowed entry, so a two-entry archive whose directory outgrows 1 KiB times the cap is refused even
// though its record count is fine.
func TestZipDirectoryOverOneKiBPerAllowedEntryIsRefused(t *testing.T) {
	t.Parallel()
	raw := paddedZip(t)
	size := int64(len(raw))
	// Refused before archive/zip builds a record, so the precheck alone must answer.
	if err := checkZipDirectory(bytes.NewReader(raw), size, walkLimits{maxEntries: 4}); !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("precheck got %v, want ErrTooManyEntries", err)
	}
	if err := ExtractFunc(bytes.NewReader(raw), size, "x.zip", Options{MaxEntries: 4}, func(string, bool, io.Reader) error { return nil }); !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("ExtractFunc got %v, want ErrTooManyEntries", err)
	}
	l, err := NewLister(bytes.NewReader(raw), size, "x.zip", ListerOptions{MaxEntries: 4})
	if err == nil {
		_, err = l.Next()
		_ = l.Close()
	}
	if !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("Lister got %v, want ErrTooManyEntries", err)
	}
	dest := t.TempDir()
	root, err := os.OpenRoot(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := Extract(bytes.NewReader(raw), size, "x.zip", root, Options{MaxEntries: 4}); !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("Extract got %v, want ErrTooManyEntries", err)
	}
	if ents, readErr := os.ReadDir(dest); readErr != nil {
		t.Fatal(readErr)
	} else if len(ents) != 0 {
		t.Fatalf("refused archive still wrote %d entries", len(ents))
	}
}

func TestZipWithinTheDirectoryBoundIsRead(t *testing.T) {
	t.Parallel()
	dest, err := extractDir(t, buildZip(t, zentry{name: "a", body: "1"}, zentry{name: "b", body: "2"}), Options{MaxEntries: 4})
	if err != nil {
		t.Fatalf("normal zip refused: %v", err)
	}
	if ents, readErr := os.ReadDir(dest); readErr != nil {
		t.Fatal(readErr)
	} else if len(ents) != 2 {
		t.Fatalf("wrote %d entries, want 2", len(ents))
	}
}

func v7tar(name, body string) []byte {
	var hdr [512]byte
	copy(hdr[:], name)
	n := len(body)
	for i := 134; i >= 124; i-- {
		hdr[i] = '0' + byte(n%8)
		n /= 8
	}
	hdr[156] = '0'
	var sum int64
	for i, c := range hdr {
		if i >= 148 && i < 156 {
			sum += ' '
			continue
		}
		sum += int64(c)
	}
	hdr[154] = 0
	hdr[155] = ' '
	for i := 153; i >= 148; i-- {
		hdr[i] = '0' + byte(sum%8)
		sum /= 8
	}
	out := append(hdr[:], body...)
	if r := len(body) % 512; r != 0 {
		out = append(out, make([]byte, 512-r)...)
	}
	return append(out, make([]byte, 1024)...)
}

func TestExtractRecognizesV7Tar(t *testing.T) {
	t.Parallel()
	raw := v7tar("hello.txt", "hello")
	var names []string
	var body bytes.Buffer
	err := ExtractFunc(bytes.NewReader(raw), int64(len(raw)), "a.tar", Options{},
		func(name string, _ bool, r io.Reader) error {
			names = append(names, name)
			_, err := body.ReadFrom(r)
			return err
		})
	if err != nil {
		t.Fatalf("ExtractFunc: %v", err)
	}
	if len(names) != 1 || names[0] != "hello.txt" || body.String() != "hello" {
		t.Fatalf("got names=%v body=%q, want hello.txt / hello", names, body.String())
	}
}

func TestExtractRecognizesGzippedV7Tar(t *testing.T) {
	t.Parallel()
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, _ = w.Write(v7tar("hello.txt", "hello"))
	_ = w.Close()
	raw := gz.Bytes()
	var names []string
	var body bytes.Buffer
	err := ExtractFunc(bytes.NewReader(raw), int64(len(raw)), "a.tar.gz", Options{},
		func(name string, _ bool, r io.Reader) error {
			names = append(names, name)
			_, err := body.ReadFrom(r)
			return err
		})
	if err != nil {
		t.Fatalf("ExtractFunc: %v", err)
	}
	if len(names) != 1 || names[0] != "hello.txt" || body.String() != "hello" {
		t.Fatalf("got names=%v body=%q, want hello.txt / hello", names, body.String())
	}
}

func TestCaseCollisionFoldsAsTheFileSystemDoes(t *testing.T) {
	t.Parallel()
	for _, pair := range [][2]string{{"a/ς.txt", "a/σ.txt"}, {"ſ.txt", "s.txt"}, {"µ.txt", "μ.txt"}} {
		x := newExtractor(Options{}, 1, nil)
		if _, err := x.admit(pair[0], false); err != nil {
			t.Fatal(err)
		}
		if _, err := x.admit(pair[1], false); !errors.Is(err, ErrCaseCollision) {
			t.Errorf("%q then %q: got %v, want ErrCaseCollision", pair[0], pair[1], err)
		}
	}
}
