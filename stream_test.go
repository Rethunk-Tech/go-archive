package archive

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestAStreamIsRecognisedOnlyFromAPlausibleHeader(t *testing.T) {
	t.Parallel()
	if looksLikeLZMA([]byte{0x5D}) {
		t.Fatal("a truncated LZMA properties byte looked like LZMA")
	}
	head := make([]byte, 13)
	head[0] = 225
	if looksLikeLZMA(head) {
		t.Fatal("properties 225 looked like LZMA")
	}
	if _, ok := tarOctal([]byte{'8'}); ok {
		t.Fatal("checksum digit 8 is not octal")
	}
}

func TestNoEntryCapGrantsNoTarFraming(t *testing.T) {
	t.Parallel()
	if got := headerAllowance(0); got != 0 {
		t.Fatalf("headerAllowance(0) = %d", got)
	}
	if got := headerAllowance(-1); got != 0 {
		t.Fatalf("headerAllowance(-1) = %d", got)
	}
}

func TestASpentStreamBudgetAtEOFIsNotACapFailure(t *testing.T) {
	t.Parallel()
	lr := &limitReader{r: bytes.NewReader(nil), left: 0, reason: ErrRatio}
	n, err := lr.Read(make([]byte, 1))
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("empty reader: got %d, %v; want EOF", n, err)
	}
	lr = &limitReader{r: stallReader{}, left: 0, reason: ErrRatio}
	n, err = lr.Read(make([]byte, 1))
	if n != 0 || err != nil {
		t.Fatalf("stall: got %d, %v; want a zero read", n, err)
	}
	if err := (&limitReader{r: bytes.NewReader(nil)}).Close(); err != nil {
		t.Fatal(err)
	}
}

type stallReader struct{}

func (stallReader) Read([]byte) (int, error) { return 0, nil }

func TestACorruptGzipHeaderFailsBeforeAnyPayload(t *testing.T) {
	t.Parallel()
	bad := []byte{0x1f, 0x8b, 0x00}
	if _, _, err := decoder(bytes.NewReader(bad), fmtGzip); err == nil {
		t.Fatal("gzip decoder started")
	}
	if _, err := openPayload(bytes.NewReader(bad), fmtGzip, "", walkLimits{budget: 64}); err == nil {
		t.Fatal("openPayload started")
	}
	if _, err := streamSize(bytes.NewReader(bad), fmtGzip, int64(len(bad))); err == nil {
		t.Fatal("streamSize started")
	}
	d := zipDecompressor(fmtGzip)(bytes.NewReader(bad))
	defer func() { _ = d.Close() }()
	if _, err := d.Read(make([]byte, 1)); err == nil {
		t.Fatal("zip method read succeeded")
	}
}

func TestADecoderPeekErrorClosesThePayload(t *testing.T) {
	t.Parallel()
	want := errors.New("peek failed")
	_, err := openPayload(errReader{want}, fmtTar, "", walkLimits{budget: 64})
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }
