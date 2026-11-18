package rardecode

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestRangeCoder(t *testing.T) {
	t.Parallel()
	var r rangeCoder
	if err := r.init(bytes.NewReader([]byte{1, 2})); err == nil {
		t.Fatal("truncated init")
	}
	if err := r.init(bytes.NewReader([]byte{1, 2, 3, 4})); err != nil {
		t.Fatal(err)
	}
	_ = r.currentCount(4)
	if err := r.decode(0, 1); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	r.low = 0
	r.rnge = rangeBottom - 1
	r.br = bytes.NewReader(nil)
	if err := r.normalize(); err == nil {
		t.Fatal("normalize eof")
	}
	r.br = bytes.NewReader([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	r.low = 0
	r.rnge = rangeBottom - 1
	if err := r.normalize(); err != nil && err != io.EOF {
		t.Fatal(err)
	}
}

func TestSee2Context(t *testing.T) {
	t.Parallel()
	var nilSee *see2Context
	if nilSee.mean() != 1 {
		t.Fatal("nil mean")
	}
	nilSee.update()

	s := newSee2Context(0)
	if s.mean() != 1 {
		t.Fatal("zero mean")
	}
	s = newSee2Context(8)
	_ = s.mean()
	for i := 0; i < 20; i++ {
		s.update()
	}
	s.shift = periodBits
	s.update()
}

func TestStateAndSuccContext(t *testing.T) {
	t.Parallel()
	var s state
	s.setUint16(0xABCD)
	if s.uint16() != 0xABCD || s.sym != 0xCD || s.freq != 0xAB {
		t.Fatalf("%#v", s)
	}
	if succContext(0) != 0 || succContext(-3) != 0 || succContext(4) != 4 {
		t.Fatal("succContext")
	}
}

func TestSubAllocator(t *testing.T) {
	t.Parallel()
	var a subAllocator
	a.init(2)
	a.init(1) // reuse backing store
	a.restart()

	for i := 0; i < 8; i++ {
		idx := a.pushByte(byte(i + 1))
		if idx == 0 && i < 6 {
			t.Fatalf("push %d", i)
		}
		if idx != 0 && a.succByte(idx+1) != byte(i+1) {
			t.Fatalf("succByte %d", i)
		}
	}
	a.popByte()
	_ = a.nextByteAddr(-3)

	a.heap1Lo = a.heap1Hi
	if a.pushByte(9) != 0 {
		t.Fatal("overflow push")
	}

	n := a.allocUnits(3)
	if n == 0 {
		t.Fatal("alloc")
	}
	a.freeUnits(n, index2Units[3])
	if a.removeFreeBlock(3) == 0 {
		t.Fatal("free list")
	}

	c := a.newContext(state{sym: 7, freq: 3}, 0)
	if c == 0 {
		t.Fatal("newContext")
	}
	if a.contextNumStates(c) != 1 {
		t.Fatal("numStates")
	}
	cs := a.newContextSize(4)
	if len(a.contextStates(cs)) != 4 {
		t.Fatal("newContextSize")
	}
	a.contextSetSummFreq(cs, 10)
	a.contextIncSummFreq(cs, 2)
	if a.contextSummFreq(cs) != 12 {
		t.Fatal("summFreq")
	}

	exp := a.expandStates(c)
	if len(exp) != 2 {
		t.Fatalf("expand ns=1 -> %d", len(exp))
	}
	exp = a.expandStates(c)
	if len(exp) != 3 {
		t.Fatalf("expand even -> %d", len(exp))
	}
	sh := a.shrinkStates(c, a.contextStates(c), 1)
	if len(sh) != 1 {
		t.Fatalf("shrink %d", len(sh))
	}

	c2 := a.newContextSize(6)
	st := a.contextStates(c2)
	st[0].sym = 'a'
	st[3].sym = 'z'
	if a.findState(c2, 'z').sym != 'z' {
		t.Fatal("findState")
	}

	big := a.allocUnits(10)
	a.addFreeBlock(big, 10)
	a.heap2Lo = a.heap2Hi
	a.glueCount = 0
	if a.allocUnits(2) == 0 {
		// heap1 may still supply a block
	}

	a.glueCount = 0
	a.heap2Lo = a.heap2Hi
	clear(a.freeList[:])
	_ = a.allocUnitsRare(0)

	_ = a.contextSuffix(c)
}

func TestModelInitRestartRead(t *testing.T) {
	t.Parallel()
	var m model
	if err := m.init(bytes.NewReader([]byte{1}), true, 4, 1); err == nil {
		t.Fatal("truncated rc")
	}
	if err := m.init(bytes.NewReader([]byte{1, 2, 3, 4}), false, 4, 1); err != nil {
		t.Fatal(err)
	}
	if err := m.init(bytes.NewReader([]byte{1, 2, 3, 4}), true, 1, 1); !errors.Is(err, ErrCorruptPPM) {
		t.Fatalf("maxOrder 1: %v", err)
	}
	if err := m.init(bytes.NewReader(bytes.Repeat([]byte{0x5A}, 16)), true, 4, 1); err != nil {
		t.Fatal(err)
	}

	m.maxOrder = 4
	m.restart()
	m.maxOrder = 16
	m.restart()

	streams := [][]byte{
		bytes.Repeat([]byte{0}, 48),
		bytes.Repeat([]byte{0xFF}, 48),
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 0x80, 0x40, 0x20, 0x10},
	}
	for _, in := range streams {
		var mm model
		if err := mm.init(bytes.NewReader(in), true, 8, 1); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 24; i++ {
			if _, err := mm.ReadByte(); err != nil {
				break
			}
		}
	}

	var empty model
	empty.maxOrder = 4
	empty.a.init(1)
	empty.rc.br = bytes.NewReader(nil)
	empty.rc.rnge = 257 * 4
	if _, err := empty.ReadByte(); err == nil {
		t.Fatal("empty read")
	}
}

func TestModelRescaleAndDecode(t *testing.T) {
	t.Parallel()
	var m model
	if err := m.init(bytes.NewReader(bytes.Repeat([]byte{1}, 8)), true, 6, 1); err != nil {
		t.Fatal(err)
	}
	m.restart()
	m.rc.br = bytes.NewReader(bytes.Repeat([]byte{0}, 32))
	m.rc.low = 0
	m.rc.rnge = ^uint32(0)
	m.rc.code = 0

	c := m.a.newContext(state{sym: 'x', freq: 200}, m.c)
	m.a.contextSetSummFreq(c, 210)
	_ = m.rescale(c, &m.a.states[c+1])

	c2 := m.a.newContextSize(4)
	st := m.a.contextStates(c2)
	for i := range st {
		st[i] = state{sym: byte(i), freq: 200}
	}
	m.a.contextSetSummFreq(c2, 900)
	m.orderFall = 1
	_ = m.rescale(c2, &st[0])

	z := m.a.newContext(state{sym: 1, freq: 1}, m.c)
	m.a.contextSetSummFreq(z, 0)
	if _, err := m.decodeSymbol1(z); !errors.Is(err, ErrCorruptPPM) {
		t.Fatalf("zero scale: %v", err)
	}

	one := m.a.newContext(state{sym: 'a', freq: 2, succ: -1}, m.c)
	m.rc.rnge = binScale * 4
	m.rc.code = 0
	m.rc.low = 0
	if _, err := m.decodeBinSymbol(one); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	m.rc.rnge = binScale * 4
	m.rc.code = binScale * 3
	m.rc.low = 0
	if _, err := m.decodeBinSymbol(one); err != nil && err != io.EOF {
		t.Fatal(err)
	}

	root := m.c
	small := m.a.newContextSize(8)
	m.a.contextSetSummFreq(small, 20)
	see := m.makeEscFreq(small, 1)
	if see == nil {
		t.Fatal("see")
	}
	if m.makeEscFreq(root, 0) != nil && m.a.contextNumStates(root) == 256 {
		t.Fatal("ns=256 should skip see")
	}
	m.prevSym = 64
	_ = m.makeEscFreq(small, 6)

	stSmall := m.a.contextStates(small)
	for i := range stSmall {
		stSmall[i].sym = byte(i + 1)
		stSmall[i].freq = 2
	}
	m.escCount = 1
	clear(m.charMask[:])
	m.charMask[stSmall[0].sym] = m.escCount
	m.rc.br = bytes.NewReader(bytes.Repeat([]byte{0}, 16))
	m.rc.rnge = 1 << 24
	m.rc.code = 0
	m.rc.low = 0
	if _, err := m.decodeSymbol2(small, 1); err != nil && err != io.EOF && !errors.Is(err, ErrCorruptPPM) {
		t.Fatalf("decodeSymbol2: %v", err)
	}

	s := &m.a.contextStates(small)[0]
	_ = m.createSuccessors(small, s, nil)
	_ = m.update(small, small, s)
}
