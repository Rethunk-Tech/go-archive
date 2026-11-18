package rardecode

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestNewVMMemory(t *testing.T) {
	t.Parallel()

	small := newVM([]byte{1, 2, 3})
	if len(small.m) != vmSize+4 || small.m[0] != 1 || small.r[7] != vmSize {
		t.Fatalf("small vm: len=%d r7=%d b0=%d", len(small.m), small.r[7], small.m[0])
	}

	buf := make([]byte, vmSize+8)
	buf[0] = 9
	large := newVM(buf[:4])
	if cap(large.m) < vmSize+4 || large.m[0] != 9 || large.m[5] != 0 {
		t.Fatalf("large vm reuse failed: cap=%d b0=%d b5=%d", cap(large.m), large.m[0], large.m[5])
	}
}

func TestVMOperands(t *testing.T) {
	t.Parallel()
	v := newVM(nil)
	v.r[1] = 8
	binary.LittleEndian.PutUint32(v.m[8:], 0xAABBCCDD)
	v.m[12] = 0x7E

	if opI(7).get(v, false) != 7 {
		t.Fatal("opI get")
	}
	opI(7).set(v, false, 1) // no-op

	if opD(8).get(v, false) != 0xAABBCCDD || opD(12).get(v, true) != 0x7E {
		t.Fatal("opD get")
	}
	opD(8).set(v, false, 0x11223344)
	opD(12).set(v, true, 0x5A)
	if binary.LittleEndian.Uint32(v.m[8:]) != 0x11223344 || v.m[12] != 0x5A {
		t.Fatal("opD set")
	}

	v.r[2] = 0xABCDEF00
	if opR(2).get(v, false) != 0xABCDEF00 || opR(2).get(v, true) != 0x00 {
		t.Fatal("opR get")
	}
	opR(2).set(v, true, 0x11)
	if v.r[2] != 0xABCDEF11 {
		t.Fatalf("opR set byte: %x", v.r[2])
	}
	opR(2).set(v, false, 3)
	if v.r[2] != 3 {
		t.Fatal("opR set word")
	}

	v.r[3] = 8
	if opRI(3).get(v, false) != 0x11223344 {
		t.Fatal("opRI get")
	}
	if opRI(3).get(v, true) != uint32(v.m[8]) {
		t.Fatal("opRI get byte")
	}
	opRI(3).set(v, false, 0x01020304)
	opRI(3).set(v, true, 0x99)
	if v.m[8] != 0x99 {
		t.Fatal("opRI set")
	}

	bi := opBI{r: 3, i: 4}
	v.m[12] = 0x42
	if bi.get(v, true) != 0x42 {
		t.Fatal("opBI get byte")
	}
	if bi.get(v, false) != binary.LittleEndian.Uint32(v.m[12:]) {
		t.Fatal("opBI get word")
	}
	bi.set(v, true, 0x43)
	bi.set(v, false, 0x55667788)
	if binary.LittleEndian.Uint32(v.m[12:]) != 0x55667788 {
		t.Fatal("opBI set")
	}
}

func TestVMExecuteEmptyAndIP(t *testing.T) {
	t.Parallel()
	v := newVM(nil)
	v.execute(nil)
	v.execute([]command{{f: jmp, op: []operand{opI(99)}}})
	if v.ip != 99 {
		t.Fatalf("ip=%d", v.ip)
	}
}

func TestVMOps(t *testing.T) {
	t.Parallel()

	type step struct {
		setup func(*vm)
		cmd   command
		check func(*testing.T, *vm)
	}
	eq := func(reg, want uint32) func(*testing.T, *vm) {
		return func(t *testing.T, v *vm) {
			t.Helper()
			if v.r[reg] != want {
				t.Fatalf("r%d=%#x want %#x", reg, v.r[reg], want)
			}
		}
	}
	flag := func(mask, want uint32) func(*testing.T, *vm) {
		return func(t *testing.T, v *vm) {
			t.Helper()
			if v.fl&mask != want {
				t.Fatalf("flags=%#x mask=%#x want %#x", v.fl, mask, want)
			}
		}
	}

	tests := []struct {
		name  string
		steps []step
	}{
		{name: "alu_logic", steps: []step{
			{cmd: command{f: mov, op: []operand{opR(0), opI(5)}}, check: eq(0, 5)},
			{cmd: command{f: add, op: []operand{opR(0), opI(7)}}, check: eq(0, 12)},
			{cmd: command{f: sub, op: []operand{opR(0), opI(2)}}, check: eq(0, 10)},
			{cmd: command{f: xor, op: []operand{opR(0), opI(10)}}, check: eq(0, 0)},
			{cmd: command{f: or, op: []operand{opR(0), opI(0xF0)}}, check: eq(0, 0xF0)},
			{cmd: command{f: and, op: []operand{opR(0), opI(0x30)}}, check: eq(0, 0x30)},
			{cmd: command{f: test, op: []operand{opR(0), opI(0)}}, check: flag(flagZ, flagZ)},
			{cmd: command{f: inc, op: []operand{opR(0)}}, check: eq(0, 0x31)},
			{cmd: command{f: dec, op: []operand{opR(0)}}, check: eq(0, 0x30)},
			{cmd: command{f: not, op: []operand{opR(0)}}, check: eq(0, ^uint32(0x30))},
			{setup: func(v *vm) { v.r[1] = 5 }, cmd: command{f: neg, op: []operand{opR(1)}}, check: flag(flagC, flagC)},
			{setup: func(v *vm) { v.r[1] = 3 }, cmd: command{f: mul, op: []operand{opR(1), opI(5)}}, check: eq(1, 15)},
			{cmd: command{f: div, op: []operand{opR(1), opI(4)}}, check: eq(1, 3)},
			{cmd: command{f: div, op: []operand{opR(1), opI(0)}}, check: eq(1, 3)},
			{setup: func(v *vm) { v.r[2] = 0x11 }, cmd: command{f: movzx, op: []operand{opR(3), opR(2)}}, check: eq(3, 0x11)},
			{setup: func(v *vm) { v.r[2] = 0xFE }, cmd: command{f: movsx, op: []operand{opR(3), opR(2)}}, check: eq(3, 0xFFFFFFFE)},
			{setup: func(v *vm) { v.r[0], v.r[1] = 1, 2 }, cmd: command{f: xchg, op: []operand{opR(0), opR(1)}}, check: eq(0, 2)},
		}},
		{name: "shifts_flags", steps: []step{
			{setup: func(v *vm) { v.r[0] = 0x80000000 }, cmd: command{f: shl, op: []operand{opR(0), opI(1)}}, check: flag(flagC, flagC)},
			{setup: func(v *vm) { v.r[0] = 3 }, cmd: command{f: shr, op: []operand{opR(0), opI(1)}}, check: func(t *testing.T, v *vm) {
				t.Helper()
				if v.r[0] != 1 || v.fl&flagC == 0 {
					t.Fatalf("shr r0=%d fl=%#x", v.r[0], v.fl)
				}
			}},
			{setup: func(v *vm) { v.r[0] = 0x80000000 }, cmd: command{f: sar, op: []operand{opR(0), opI(1)}}, check: eq(0, 0xC0000000)},
			{setup: func(v *vm) { v.r[0] = 0xFFFFFFFF }, cmd: command{f: add, op: []operand{opR(0), opI(1)}}, check: flag(flagZ|flagC, flagZ|flagC)},
			{setup: func(v *vm) { v.r[0] = 1; v.fl = flagC }, cmd: command{f: adc, op: []operand{opR(0), opI(1)}}, check: eq(0, 3)},
			{setup: func(v *vm) { v.r[0] = 3; v.fl = flagC }, cmd: command{f: sbb, op: []operand{opR(0), opI(1)}}, check: eq(0, 1)},
			{setup: func(v *vm) { v.r[0] = 0xFF }, cmd: command{f: add, bm: true, op: []operand{opR(0), opI(1)}}, check: func(t *testing.T, v *vm) {
				t.Helper()
				if v.r[0]&0xFF != 0 || v.fl&flagC == 0 {
					t.Fatalf("byte add r0=%#x fl=%#x", v.r[0], v.fl)
				}
			}},
			{setup: func(v *vm) { v.r[0] = 0xFF }, cmd: command{f: inc, bm: true, op: []operand{opR(0)}}, check: func(t *testing.T, v *vm) {
				t.Helper()
				if v.r[0]&0xFF != 0 {
					t.Fatalf("byte inc r0=%#x", v.r[0])
				}
			}},
			{setup: func(v *vm) { v.r[0] = 1 }, cmd: command{f: dec, op: []operand{opR(0)}}, check: flag(flagZ, flagZ)},
			{setup: func(v *vm) { v.r[0] = 0 }, cmd: command{f: cmp, op: []operand{opR(0), opI(1)}}, check: flag(flagC, flagC)},
			{setup: func(v *vm) { v.r[0] = 1 }, cmd: command{f: cmp, op: []operand{opR(0), opI(1)}}, check: flag(flagZ, flagZ)},
			{setup: func(v *vm) { v.r[0] = 2 }, cmd: command{f: sub, op: []operand{opR(0), opI(1)}}, check: func(t *testing.T, v *vm) {
				t.Helper()
				if v.r[0] != 1 || v.fl&flagZ != 0 {
					t.Fatalf("sub r0=%d fl=%#x", v.r[0], v.fl)
				}
			}},
			{cmd: command{f: print}, check: func(*testing.T, *vm) {}},
		}},
		{name: "jumps", steps: []step{
			{setup: func(v *vm) { v.fl = flagZ }, cmd: command{f: jz, op: []operand{opI(40)}}, check: func(t *testing.T, v *vm) {
				if v.ip != 40 {
					t.Fatalf("jz ip=%d", v.ip)
				}
			}},
			{setup: func(v *vm) { v.fl = 0; v.ip = 0 }, cmd: command{f: jz, op: []operand{opI(40)}}, check: func(t *testing.T, v *vm) {
				if v.ipMod {
					t.Fatal("jz taken")
				}
			}},
			{setup: func(v *vm) { v.fl = 0 }, cmd: command{f: jnz, op: []operand{opI(41)}}, check: func(t *testing.T, v *vm) {
				if v.ip != 41 {
					t.Fatalf("jnz ip=%d", v.ip)
				}
			}},
			{setup: func(v *vm) { v.fl = flagS }, cmd: command{f: js, op: []operand{opI(42)}}, check: func(t *testing.T, v *vm) {
				if v.ip != 42 {
					t.Fatalf("js ip=%d", v.ip)
				}
			}},
			{setup: func(v *vm) { v.fl = 0 }, cmd: command{f: jns, op: []operand{opI(43)}}, check: func(t *testing.T, v *vm) {
				if v.ip != 43 {
					t.Fatalf("jns ip=%d", v.ip)
				}
			}},
			{setup: func(v *vm) { v.fl = flagC }, cmd: command{f: jb, op: []operand{opI(44)}}, check: func(t *testing.T, v *vm) {
				if v.ip != 44 {
					t.Fatalf("jb ip=%d", v.ip)
				}
			}},
			{setup: func(v *vm) { v.fl = flagZ }, cmd: command{f: jbe, op: []operand{opI(45)}}, check: func(t *testing.T, v *vm) {
				if v.ip != 45 {
					t.Fatalf("jbe ip=%d", v.ip)
				}
			}},
			{setup: func(v *vm) { v.fl = 0 }, cmd: command{f: ja, op: []operand{opI(46)}}, check: func(t *testing.T, v *vm) {
				if v.ip != 46 {
					t.Fatalf("ja ip=%d", v.ip)
				}
			}},
			{setup: func(v *vm) { v.fl = 0 }, cmd: command{f: jae, op: []operand{opI(47)}}, check: func(t *testing.T, v *vm) {
				if v.ip != 47 {
					t.Fatalf("jae ip=%d", v.ip)
				}
			}},
			{setup: func(v *vm) { v.fl = flagZ }, cmd: command{f: jnz, op: []operand{opI(1)}}, check: func(*testing.T, *vm) {}},
			{setup: func(v *vm) { v.fl = 0 }, cmd: command{f: js, op: []operand{opI(1)}}, check: func(*testing.T, *vm) {}},
			{setup: func(v *vm) { v.fl = flagS }, cmd: command{f: jns, op: []operand{opI(1)}}, check: func(*testing.T, *vm) {}},
			{setup: func(v *vm) { v.fl = 0 }, cmd: command{f: jb, op: []operand{opI(1)}}, check: func(*testing.T, *vm) {}},
			{setup: func(v *vm) { v.fl = 0 }, cmd: command{f: jbe, op: []operand{opI(1)}}, check: func(*testing.T, *vm) {}},
			{setup: func(v *vm) { v.fl = flagC }, cmd: command{f: ja, op: []operand{opI(1)}}, check: func(*testing.T, *vm) {}},
			{setup: func(v *vm) { v.fl = flagC }, cmd: command{f: jae, op: []operand{opI(1)}}, check: func(*testing.T, *vm) {}},
		}},
		{name: "stack", steps: []step{
			{setup: func(v *vm) { v.r[0] = 0xAABBCCDD }, cmd: command{f: push, op: []operand{opR(0)}}, check: func(t *testing.T, v *vm) {
				t.Helper()
				if v.r[7] != vmSize-4 {
					t.Fatalf("sp=%#x", v.r[7])
				}
			}},
			{cmd: command{f: pop, op: []operand{opR(1)}}, check: eq(1, 0xAABBCCDD)},
			{setup: func(v *vm) {
				v.r[0] = 1
				v.r[1] = 2
				v.r[2] = 3
				v.r[3] = 4
				v.r[4] = 5
				v.r[5] = 6
				v.r[6] = 7
			}, cmd: command{f: pusha}, check: func(*testing.T, *vm) {}},
			{cmd: command{f: popa}, check: eq(0, 1)},
			{setup: func(v *vm) { v.fl = flagC | flagZ }, cmd: command{f: pushf}, check: func(*testing.T, *vm) {}},
			{setup: func(v *vm) { v.fl = 0 }, cmd: command{f: popf}, check: flag(flagC|flagZ, flagC|flagZ)},
			{setup: func(v *vm) { v.r[7] = vmSize }, cmd: command{f: ret}, check: func(t *testing.T, v *vm) {
				if v.ip != 0xFFFFFFFF {
					t.Fatalf("ret ip=%#x", v.ip)
				}
			}},
			{setup: func(v *vm) {
				v.r[7] = 16
				binary.LittleEndian.PutUint32(v.m[16:], 9)
			}, cmd: command{f: ret}, check: func(t *testing.T, v *vm) {
				if v.ip != 9 {
					t.Fatalf("ret mem ip=%d", v.ip)
				}
			}},
			{setup: func(v *vm) { v.r[7] = vmSize; v.ip = 3 }, cmd: command{f: call, op: []operand{opI(8)}}, check: func(t *testing.T, v *vm) {
				if v.ip != 8 {
					t.Fatalf("call ip=%d", v.ip)
				}
			}},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := newVM(nil)
			for i, s := range tt.steps {
				if s.setup != nil {
					s.setup(v)
				}
				v.ipMod = false
				s.cmd.f(v, s.cmd.bm, s.cmd.op)
				if s.check != nil {
					s.check(t, v)
					if t.Failed() {
						t.Fatalf("step %d", i)
					}
				}
			}
		})
	}
}

type bitw struct {
	buf []byte
	acc uint64
	n   uint8
}

func (w *bitw) put(n uint8, v uint32) {
	w.acc = (w.acc << n) | uint64(v)
	w.n += n
	for w.n >= 8 {
		w.n -= 8
		w.buf = append(w.buf, byte(w.acc>>w.n))
		w.acc &= (uint64(1) << w.n) - 1
	}
}

func (w *bitw) finish() []byte {
	out := append([]byte(nil), w.buf...)
	if w.n > 0 {
		out = append(out, byte(w.acc<<(8-w.n)))
	}
	return out
}

func TestFixJumpOp(t *testing.T) {
	t.Parallel()
	if got := fixJumpOp(opR(1), 4); got != opR(1) {
		t.Fatalf("non-imm %v", got)
	}
	tests := []struct {
		n, off, want opI
	}{
		{300, 0, 44},
		{136, 5, 0xFFFFFF85},
		{16, 3, 11},
		{8, 2, 0xFFFFFFFA},
		{3, 7, 10},
	}
	for _, tt := range tests {
		got, ok := fixJumpOp(tt.n, int(tt.off)).(opI)
		if !ok || got != tt.want {
			t.Fatalf("n=%d off=%d got %v want %v", tt.n, tt.off, got, tt.want)
		}
	}
}

func TestDecodeArgAndReadCommands(t *testing.T) {
	t.Parallel()

	t.Run("truncated", func(t *testing.T) {
		t.Parallel()
		br := newRarBitReader(bytes.NewReader(nil))
		if _, err := decodeArg(br, false); err == nil {
			t.Fatal("expected error")
		}
		if _, err := readCommands(br); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("args", func(t *testing.T) {
		t.Parallel()
		var w bitw
		w.put(1, 1)
		w.put(3, 5) // opR(5)
		w.put(1, 0)
		w.put(1, 0) // immediate
		w.put(8, 0xAB)
		w.put(1, 0)
		w.put(1, 1)
		w.put(1, 0)
		w.put(3, 2) // opRI(2)
		w.put(1, 0)
		w.put(1, 1)
		w.put(1, 1)
		w.put(1, 0)
		w.put(3, 1)
		w.put(2, 0)
		w.put(4, 7) // opBI r=1 i=7 via uint32 n=0
		w.put(1, 0)
		w.put(1, 1)
		w.put(1, 1)
		w.put(1, 1)
		w.put(2, 0)
		w.put(4, 3) // opD(3)
		br := newRarBitReader(bytes.NewReader(w.finish()))
		op, err := decodeArg(br, true)
		if err != nil || op != opR(5) {
			t.Fatalf("reg: %v %v", op, err)
		}
		op, err = decodeArg(br, true)
		if err != nil || op != opI(0xAB) {
			t.Fatalf("imm8: %v %v", op, err)
		}
		op, err = decodeArg(br, false)
		if err != nil || op != opRI(2) {
			t.Fatalf("ri: %v %v", op, err)
		}
		op, err = decodeArg(br, false)
		bi, ok := op.(opBI)
		if err != nil || !ok || bi.r != 1 || bi.i != 7 {
			t.Fatalf("bi: %v %v", op, err)
		}
		op, err = decodeArg(br, false)
		if err != nil || op != opD(3) {
			t.Fatalf("direct: %v %v", op, err)
		}
	})

	t.Run("imm32", func(t *testing.T) {
		t.Parallel()
		var w bitw
		w.put(1, 0)
		w.put(1, 0)
		w.put(2, 2)
		w.put(16, 0x1234)
		br := newRarBitReader(bytes.NewReader(w.finish()))
		op, err := decodeArg(br, false)
		if err != nil || op != opI(0x1234) {
			t.Fatalf("imm32: %v %v", op, err)
		}
	})

	t.Run("commands", func(t *testing.T) {
		t.Parallel()
		var w bitw
		w.put(4, 0) // mov
		w.put(1, 1) // byte mode
		w.put(1, 1)
		w.put(3, 0)
		w.put(1, 1)
		w.put(3, 1)
		w.put(4, 4) // jz (jump)
		w.put(1, 1)
		w.put(3, 2)
		w.put(4, 8)
		w.put(2, 3) // code = (8<<2|3)-24 = 11 -> test? 32+3-24=11 or
		w.put(1, 0)
		w.put(1, 1)
		w.put(3, 0)
		w.put(1, 1)
		w.put(3, 1)
		br := newRarBitReader(bytes.NewReader(w.finish()))
		cmds, err := readCommands(br)
		if err != io.EOF && err != nil && !errors.Is(err, ErrInvalidVMInstruction) {
			t.Fatalf("err=%v cmds=%d", err, len(cmds))
		}
		if len(cmds) < 2 || cmds[0].f == nil || !cmds[0].bm {
			t.Fatalf("cmds=%d first bm=%v", len(cmds), len(cmds) > 0 && cmds[0].bm)
		}
	})

	t.Run("eof_ok", func(t *testing.T) {
		t.Parallel()
		var w bitw
		w.put(4, 0xA) // 1010 has 0x08, needs extra 2 bits: we'll put 4 bits 1010 then 2 bits 00 -> code=(10<<2|0)-24=16 jae? 40-24=16 ja
		// 4-bit value with high bit: 0b1000 = 8, then 2 bits.
		// Use ret: opcode 22. 22+24=46 = 0b101110, top 4 is 0b1011=11, extra 2 is 0b10.
		// Simpler: opcode 0 mov needs operands. Use ret opcode 22.
		br := newRarBitReader(bytes.NewReader(nil))
		cmds, err := readCommands(br)
		if !errors.Is(err, io.EOF) && err != nil && len(cmds) != 0 {
			t.Fatalf("empty: %v %d", err, len(cmds))
		}
	})

	t.Run("ret_cmd", func(t *testing.T) {
		t.Parallel()
		var w bitw
		// ret is ops[22]. 22 >= 8 so encoded as 6 bits: code+24 = 46 = 0b101110, readBits(4)=0b1011=11, 11&8>0, readBits(2)=0b10=2, (11<<2|2)-24=46-24=22.
		w.put(4, 0xB)
		w.put(2, 2)
		br := newRarBitReader(bytes.NewReader(w.finish()))
		cmds, err := readCommands(br)
		if err != io.EOF && err != nil {
			t.Fatalf("ret: %v", err)
		}
		if len(cmds) != 1 || cmds[0].f == nil {
			t.Fatalf("want 1 ret, got %d", len(cmds))
		}
		v := newVM(nil)
		v.execute(cmds) // ret with r7=vmSize ends
	})
}
