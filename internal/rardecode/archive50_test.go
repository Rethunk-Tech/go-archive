package rardecode

import (
	"bytes"
	"encoding/hex"
	"slices"
	"testing"
)

func TestFileHashRecordRoundTrip(t *testing.T) {
	t.Parallel()

	empty, err := hex.DecodeString("dd0e891776933f43c7d032b08a917e25741f8aa9a12c12e1cac8801500f2ca4f")
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte("rar5 hash extra")
	payloadSum, err := hex.DecodeString("5946ec1012366fd4a463a184b7d276d0dd6b3b2a36d1ef570c2bd3d98994fa01")
	if err != nil {
		t.Fatal(err)
	}
	long := make([]byte, 768)
	for i := range long {
		long[i] = byte(i)
	}
	longSum, err := hex.DecodeString("3b5b5cb5c58c1baf5705afdbff35d66b1cfbea9dc92f1babe8114932b6e948ab")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		extra   []byte
		data    []byte
		wantSum []byte
		wantOK  bool
	}{
		{name: "empty", extra: append([]byte{file5HashBlake2sp}, empty...), data: nil, wantSum: empty, wantOK: true},
		{name: "payload", extra: append([]byte{file5HashBlake2sp}, payloadSum...), data: payload, wantSum: payloadSum, wantOK: true},
		{name: "striped", extra: append([]byte{file5HashBlake2sp}, longSum...), data: long, wantSum: longSum, wantOK: true},
		{name: "mismatch", extra: append([]byte{file5HashBlake2sp}, empty...), data: payload, wantSum: empty, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &archive50{}
			hdr := &blockHeader50{
				data:  readBuf([]byte{0, 0, 0, 0, file5HostOSUnix, 1, 'a'}),
				extra: []extra{{ftype: file5ExtraHash, data: readBuf(slices.Clone(tt.extra))}},
			}
			f, err := a.parseFileHeader(hdr)
			if err != nil {
				t.Fatalf("parseFileHeader: %v", err)
			}
			if !bytes.Equal(f.sum, tt.wantSum) {
				t.Fatalf("stored sum = %x, want %x", f.sum, tt.wantSum)
			}
			if f.hash == nil {
				t.Fatal("hash constructor not set")
			}
			got := f.hash()
			if _, err := got.Write(tt.data); err != nil {
				t.Fatal(err)
			}
			sum := got.Sum(nil)
			if bytes.Equal(sum, f.sum) != tt.wantOK {
				t.Fatalf("verify %x vs %x, ok=%v", sum, f.sum, tt.wantOK)
			}
		})
	}
}
