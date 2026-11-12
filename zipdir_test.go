package archive

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
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
