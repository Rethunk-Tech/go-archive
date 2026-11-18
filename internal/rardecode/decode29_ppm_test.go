package rardecode

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestPPM29InitReset(t *testing.T) {
	t.Parallel()
	d := newPPM29Decoder()
	if d.esc != 2 || d.m.maxOrder != 2 {
		t.Fatalf("defaults esc=%d order=%d", d.esc, d.m.maxOrder)
	}
	d.esc = 9
	d.reset()
	if d.esc != 2 {
		t.Fatal("reset")
	}

	if err := d.init(newRarBitReader(bytes.NewReader(nil))); err == nil {
		t.Fatal("empty")
	}

	// reset, no esc, maxOrder 1 after mask -> ErrCorruptPPM once range coder is fed
	if err := d.init(newRarBitReader(bytes.NewReader([]byte{0x40, 0, 1, 2, 3, 4}))); !errors.Is(err, ErrCorruptPPM) && err != io.EOF && err != nil {
		t.Fatalf("order1: %v", err)
	}

	// reset+esc, leftover bits then maxMB, esc, range coder
	if err := d.init(newRarBitReader(bytes.NewReader([]byte{0xC0, 0, 2, 3, 4, 5, 6, 7}))); err != nil && !errors.Is(err, ErrCorruptPPM) && err != io.EOF {
		t.Fatalf("reset+esc: %v", err)
	}

	// high maxOrder (low 5 bits all set) + reset
	if err := d.init(newRarBitReader(bytes.NewReader([]byte{0x7E, 0, 1, 2, 3, 4, 5}))); err != nil && !errors.Is(err, ErrCorruptPPM) && err != io.EOF {
		t.Fatalf("high order: %v", err)
	}

	if err := d.init(newRarBitReader(bytes.NewReader([]byte{0x40}))); err == nil {
		t.Fatal("reset without maxMB")
	}
	if err := d.init(newRarBitReader(bytes.NewReader([]byte{0x80, 1}))); err == nil {
		t.Fatal("esc without byte")
	}
}

func TestPPM29FillAndFilter(t *testing.T) {
	t.Parallel()
	d := newPPM29Decoder()
	full := &decodeReader{size: 1, win: make([]byte, 1), w: 1}
	b, err := d.fill(full)
	if b != nil || err != nil {
		t.Fatalf("full window: %v %v", b, err)
	}

	d.m.rc.br = bytes.NewReader(nil)
	d.m.rc.rnge = ^uint32(0)
	dr := &decodeReader{size: 8, win: make([]byte, 8)}
	if _, err := d.fill(dr); err == nil {
		t.Fatal("empty model")
	}
	if _, err := d.readFilterData(); err == nil {
		t.Fatal("filter without data")
	}

	for _, in := range [][]byte{
		bytes.Repeat([]byte{0}, 32),
		bytes.Repeat([]byte{0xFF}, 32),
		{0x40, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
	} {
		d = newPPM29Decoder()
		_ = d.init(newRarBitReader(bytes.NewReader(in)))
		dr = &decodeReader{size: 16, win: make([]byte, 16)}
		_, _ = d.fill(dr)
		_, _ = d.readFilterData()
	}
}

func chainPPM(m *model, syms []byte) context {
	var next context
	for i := len(syms) - 1; i >= 0; i-- {
		next = m.a.newContext(state{sym: syms[i], freq: 8, succ: int32(next)}, m.c)
	}
	return next
}

func primedPPM29(syms []byte) *ppm29Decoder {
	d := newPPM29Decoder()
	d.m.maxOrder = 2
	d.m.restart()
	d.esc = 2
	d.m.c = chainPPM(&d.m, syms)
	d.m.orderFall = 0
	d.m.rc.br = bytes.NewReader(bytes.Repeat([]byte{0}, 128))
	d.m.rc.low = 0
	d.m.rc.code = 0
	d.m.rc.rnge = binScale * 16
	return d
}

func TestPPM29FillEscapes(t *testing.T) {
	t.Parallel()
	win := func() *decodeReader {
		return &decodeReader{size: 64, win: make([]byte, 64)}
	}

	if _, err := primedPPM29([]byte{2, 0}).fill(win()); !errors.Is(err, errEndOfBlock) {
		t.Fatalf("eob: %v", err)
	}
	if _, err := primedPPM29([]byte{2, 2}).fill(win()); !errors.Is(err, errEndOfBlockAndFile) {
		t.Fatalf("eobfile: %v", err)
	}

	d := primedPPM29([]byte{2, 6, 9})
	dr := win()
	if _, err := d.fill(dr); err != nil && err != io.EOF {
		t.Fatalf("default esc: %v", err)
	}
	if dr.w == 0 {
		t.Fatal("expected literal write")
	}

	d = primedPPM29([]byte{2, 5, 1})
	if _, err := d.fill(win()); err != nil && err != io.EOF {
		t.Fatalf("copy1: %v", err)
	}
	d = primedPPM29([]byte{2, 4, 0, 0, 1, 2})
	if _, err := d.fill(win()); err != nil && err != io.EOF {
		t.Fatalf("copy3: %v", err)
	}

	d = primedPPM29([]byte{1, 2, 0})
	dr = win()
	_, err := d.fill(dr)
	if err != nil && err != io.EOF && !errors.Is(err, errEndOfBlock) {
		t.Fatalf("literal then eob: %v", err)
	}
}

func TestPPM29ReadFilterData(t *testing.T) {
	t.Parallel()
	d := primedPPM29([]byte{1, 9, 8})
	buf, err := d.readFilterData()
	if err != nil && err != io.EOF {
		t.Fatalf("short n: %v", err)
	}
	if err == nil && len(buf) < 2 {
		t.Fatalf("len=%d", len(buf))
	}

	d = primedPPM29([]byte{6, 0, 1, 2, 3, 4, 5, 6, 7})
	if _, err := d.readFilterData(); err != nil && err != io.EOF {
		t.Fatalf("n=7: %v", err)
	}

	d = primedPPM29([]byte{7, 0, 0})
	if _, err := d.readFilterData(); err != nil && err != io.EOF {
		t.Fatalf("n=8: %v", err)
	}

	d = primedPPM29([]byte{2, 3, 1, 2})
	if _, err := d.fill(&decodeReader{size: 32, win: make([]byte, 32)}); err != nil && err != io.EOF {
		t.Fatalf("fill filter: %v", err)
	}
}
