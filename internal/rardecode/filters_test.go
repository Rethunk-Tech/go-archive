package rardecode

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestVMFilterOutOfRangeOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		start  uint32
		length uint32
	}{
		{name: "start_plus_length_past_vm", start: vmSize - 1, length: 2},
		{name: "uint32_wrap", start: vmMask, length: vmMask},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &vmFilter{
				code: []command{
					{f: mov, op: []operand{opD(vmGlobalAddr + 0x20), opI(tt.start)}},
					{f: mov, op: []operand{opD(vmGlobalAddr + 0x1c), opI(tt.length)}},
				},
			}
			_, err := f.execute(nil, nil, []byte("x"), 0)
			if !errors.Is(err, ErrInvalidFilter) {
				t.Fatalf("execute: %v, want ErrInvalidFilter", err)
			}
		})
	}
}

func TestFilterE8(t *testing.T) {
	t.Parallel()

	short, err := filterE8(0xe9, false, []byte{0xe8, 1, 2}, 0)
	if err != nil || len(short) != 3 {
		t.Fatalf("short: %v %d", err, len(short))
	}

	buf := []byte{0x90, 0xe8, 10, 0, 0, 0, 0xe9, 0xff, 0xff, 0xff, 0xff, 0xe8, 0, 0, 0, 0x01}
	got, err := filterE8(0xe9, false, append([]byte(nil), buf...), 4)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 0x90 {
		t.Fatalf("nop overwritten: %x", got[0])
	}

	v5 := []byte{0xe8, 20, 0, 0, 0}
	if _, err := filterE8(0xe8, true, v5, fileSize+8); err != nil {
		t.Fatal(err)
	}

	if _, err := e8FilterV3(nil, nil, []byte{0xe8, 1, 0, 0, 0}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e8e9FilterV3(nil, nil, []byte{0xe9, 1, 0, 0, 0}, 0); err != nil {
		t.Fatal(err)
	}
}

func TestGetSetBits(t *testing.T) {
	t.Parallel()
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint32(buf, 0xFFFFFFFF)
	if getBits(buf, 4, 8) != 0xFF {
		t.Fatalf("getBits=%#x", getBits(buf, 4, 8))
	}
	setBits(buf, 4, 8, 0xA5)
	if getBits(buf, 4, 8) != 0xA5 {
		t.Fatalf("setBits roundtrip %#x", getBits(buf, 4, 8))
	}
}

func TestItaniumFilterV3(t *testing.T) {
	t.Parallel()
	tiny, err := itaniumFilterV3(nil, nil, make([]byte, 16), 0)
	if err != nil || len(tiny) != 16 {
		t.Fatalf("tiny: %v %d", err, len(tiny))
	}

	buf := make([]byte, 32)
	buf[0] = 0x10  // c=0, mask=4 (bit 2)
	buf[15] = 0x50 // getBits(pos=124,4)==5
	if _, err := itaniumFilterV3(nil, nil, buf, 16); err != nil {
		t.Fatal(err)
	}

	buf2 := make([]byte, 32)
	buf2[0] = 0x14 // c=4, mask=0
	if _, err := itaniumFilterV3(nil, nil, buf2, 0); err != nil {
		t.Fatal(err)
	}

	buf3 := make([]byte, 32)
	buf3[0] = 0x0F // c=-1, skip
	if _, err := itaniumFilterV3(nil, nil, buf3, 0); err != nil {
		t.Fatal(err)
	}
}

func TestFilterDelta(t *testing.T) {
	t.Parallel()
	in := []byte{1, 2, 3, 4}
	got, err := filterDelta(2, in)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{255, 253, 253, 249}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	grown := make([]byte, 4, 8)
	copy(grown, in)
	if _, err := filterDelta(1, grown); err != nil {
		t.Fatal(err)
	}
	if _, err := deltaFilterV3(map[int]uint32{0: 2}, nil, append([]byte(nil), in...), 0); err != nil {
		t.Fatal(err)
	}
}

func TestFilterRGBV3(t *testing.T) {
	t.Parallel()
	if abs(-3) != 3 || abs(4) != 4 {
		t.Fatal("abs")
	}
	early, err := filterRGBV3(map[int]uint32{0: 0, 1: 0}, nil, []byte{1, 2, 3}, 0)
	if err != nil || len(early) != 3 {
		t.Fatalf("width wrap: %v %d", err, len(early))
	}
	in := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	if _, err := filterRGBV3(map[int]uint32{0: 6, 1: 0}, nil, in, 0); err != nil {
		t.Fatal(err)
	}
	caped := make([]byte, len(in), 2*len(in))
	copy(caped, in)
	if _, err := filterRGBV3(map[int]uint32{0: 6, 1: 1}, nil, caped, 0); err != nil {
		t.Fatal(err)
	}
}

func TestFilterAudioV3(t *testing.T) {
	t.Parallel()
	in := make([]byte, 40)
	for i := range in {
		in[i] = byte(i * 3)
	}
	if _, err := filterAudioV3(map[int]uint32{0: 1}, nil, in, 0); err != nil {
		t.Fatal(err)
	}
	caped := make([]byte, 8, 16)
	if _, err := filterAudioV3(map[int]uint32{0: 2}, nil, caped, 0); err != nil {
		t.Fatal(err)
	}
}

func TestFilterArm(t *testing.T) {
	t.Parallel()
	buf := []byte{0x01, 0x02, 0x03, 0xeb, 0x00, 0x00, 0x00, 0x00}
	got, err := filterArm(append([]byte(nil), buf...), 8)
	if err != nil {
		t.Fatal(err)
	}
	if got[3] != 0xeb {
		t.Fatalf("marker %x", got[3])
	}
	if bytes.Equal(got[:3], buf[:3]) {
		t.Fatal("arm offset not rewritten")
	}
}

func TestVMFilterExecutePaths(t *testing.T) {
	t.Parallel()
	f := &vmFilter{code: []command{{f: ret}}}
	if _, err := f.execute(nil, nil, make([]byte, vmGlobalAddr+1), 0); !errors.Is(err, ErrInvalidFilter) {
		t.Fatalf("oversize: %v", err)
	}

	f = &vmFilter{
		global: make([]byte, 4),
		static: []byte{9},
		code: []command{
			{f: mov, op: []operand{opD(vmGlobalAddr + 0x30), opI(4)}},
			{f: mov, op: []operand{opD(vmGlobalAddr + 0x20), opI(4)}},
			{f: mov, op: []operand{opD(vmGlobalAddr + 0x1c), opI(2)}},
			{f: ret},
		},
	}
	out, err := f.execute(map[int]uint32{0: 7}, make([]byte, 8), []byte{1, 2, 3, 4, 5, 6}, 11)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("out len %d", len(out))
	}
	if f.execCount != 1 || len(f.global) != 4 {
		t.Fatalf("execCount=%d global=%d", f.execCount, len(f.global))
	}

	f.global = nil
	f.code = []command{
		{f: mov, op: []operand{opD(vmGlobalAddr + 0x30), opI(vmGlobalSize)}},
		{f: ret},
	}
	if _, err := f.execute(nil, []byte{1, 2}, []byte("ab"), 0); err != nil {
		t.Fatal(err)
	}
	if len(f.global) != int(vmGlobalSize-vmFixedGlobalSize) {
		t.Fatalf("capped global %d", len(f.global))
	}
}

func TestGetV3Filter(t *testing.T) {
	t.Parallel()

	if _, err := getV3Filter([]byte{0}); err == nil {
		t.Fatal("truncated")
	}

	var truncStatic bitw
	truncStatic.put(1, 1) // has static, no uint32
	if _, err := getV3Filter(append([]byte{0}, truncStatic.finish()...)); err == nil {
		t.Fatal("truncated static length")
	}
	var truncBody bitw
	truncBody.put(1, 1)
	truncBody.put(2, 0)
	truncBody.put(4, 3) // m=3 -> 4 bytes, none supplied
	if _, err := getV3Filter(append([]byte{0}, truncBody.finish()...)); err == nil {
		t.Fatal("truncated static body")
	}

	var w bitw
	w.put(1, 0) // no static
	code := append([]byte{0}, w.finish()...)
	f, err := getV3Filter(code)
	if err != nil || f == nil {
		t.Fatalf("empty program: %v", err)
	}

	var w2 bitw
	w2.put(1, 1)
	w2.put(2, 0)
	w2.put(4, 0) // m=0 -> 1 byte static
	w2.put(8, 0xAB)
	code = append([]byte{0}, w2.finish()...)
	f, err = getV3Filter(code)
	if err != nil && err != io.EOF {
		t.Fatalf("static: %v", err)
	}
	if f == nil {
		t.Fatal("nil filter")
	}

	var w3 bitw
	w3.put(1, 1)
	w3.put(2, 3)
	w3.put(32, 0x1FC0) // m+1 too large after ++? n=3 reads 32 bits; m=0x1FC0, m++ = 0x1FC1 > 0x1FC0
	code = append([]byte{0}, w3.finish()...)
	if _, err := getV3Filter(code); !errors.Is(err, ErrInvalidFilter) && err == nil {
		t.Fatalf("want invalid or truncated, got %v", err)
	}

	_, _ = getV3Filter(bytes.Repeat([]byte{1}, 29))
}
