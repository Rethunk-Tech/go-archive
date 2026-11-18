package rardecode

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"testing"
)

type bitWriter struct {
	b []byte
	v uint64
	n int
}

func (w *bitWriter) put(val, nbits int) {
	w.v = (w.v << nbits) | uint64(val)&uint64((1<<nbits)-1)
	w.n += nbits
	for w.n >= 8 {
		w.n -= 8
		w.b = append(w.b, byte(w.v>>w.n))
		w.v &= (1 << w.n) - 1
	}
}

func (w *bitWriter) bytes() []byte {
	if w.n == 0 {
		return w.b
	}
	return append(append([]byte{}, w.b...), byte(w.v<<(8-w.n)))
}

type memFile struct{ *bytes.Reader }

func (memFile) writeToN(io.Writer, int64) (int64, error) { return 0, nil }
func (memFile) currFile() *fileBlockHeader               { return nil }
func (memFile) nextFile() (*fileBlockList, error)        { return nil, io.EOF }
func (memFile) newArchiveFile(*fileBlockList) (archiveFile, error) {
	return nil, io.EOF
}
func (memFile) Stat() (fs.FileInfo, error) { return nil, errors.New("stat") }

func bitReaderFrom(b []byte) *rarBitReader {
	return newRarBitReader(bytes.NewReader(b))
}

func newDecodeWindow() *decodeReader {
	const n = 64
	return &decodeReader{win: make([]byte, n), size: n}
}

func oneSym(n, sym int, length byte) []byte {
	l := make([]byte, n)
	l[sym] = length
	return l
}

func table20Bits(bitlen [19]byte, payload func(*bitWriter)) []byte {
	var w bitWriter
	for _, v := range bitlen {
		w.put(int(v), 4)
	}
	if payload != nil {
		payload(&w)
	}
	return w.bytes()
}

func TestReadCodeLengthTable20(t *testing.T) {
	t.Parallel()

	t.Run("truncated bit lengths", func(t *testing.T) {
		err := readCodeLengthTable20(bitReaderFrom(nil), make([]byte, 4))
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("truncated symbols", func(t *testing.T) {
		var bitlen [19]byte
		bitlen[0] = 1
		err := readCodeLengthTable20(bitReaderFrom(table20Bits(bitlen, nil)), make([]byte, 8))
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("literal lengths", func(t *testing.T) {
		var bitlen [19]byte
		bitlen[3] = 1
		b := table20Bits(bitlen, func(w *bitWriter) {
			for i := 0; i < 4; i++ {
				w.put(0, 1)
			}
		})
		table := make([]byte, 4)
		if err := readCodeLengthTable20(bitReaderFrom(b), table); err != nil {
			t.Fatal(err)
		}
		for i, v := range table {
			if v != 3 {
				t.Fatalf("table[%d]=%d, want 3", i, v)
			}
		}
	})

	t.Run("repeat at start", func(t *testing.T) {
		var bitlen [19]byte
		bitlen[16] = 1
		b := table20Bits(bitlen, func(w *bitWriter) { w.put(0, 1) })
		err := readCodeLengthTable20(bitReaderFrom(b), make([]byte, 4))
		if !errors.Is(err, ErrInvalidLengthTable) {
			t.Fatalf("got %v, want ErrInvalidLengthTable", err)
		}
	})

	t.Run("repeat truncated extra", func(t *testing.T) {
		var bitlen [19]byte
		bitlen[1] = 2
		bitlen[16] = 2
		b := table20Bits(bitlen, func(w *bitWriter) {
			w.put(0, 2)
			w.put(1, 2)
		})
		err := readCodeLengthTable20(bitReaderFrom(b), make([]byte, 8))
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("repeat previous", func(t *testing.T) {
		var bitlen [19]byte
		bitlen[7] = 2
		bitlen[16] = 2
		b := table20Bits(bitlen, func(w *bitWriter) {
			w.put(0, 2)
			w.put(1, 2)
			w.put(0, 2)
		})
		table := make([]byte, 4)
		if err := readCodeLengthTable20(bitReaderFrom(b), table); err != nil {
			t.Fatal(err)
		}
		for i, v := range table {
			if v != 7 {
				t.Fatalf("table[%d]=%d, want 7", i, v)
			}
		}
	})

	t.Run("zeros 17 truncated extra", func(t *testing.T) {
		var bitlen [19]byte
		bitlen[17] = 2
		b := table20Bits(bitlen, func(w *bitWriter) { w.put(0, 2) })
		err := readCodeLengthTable20(bitReaderFrom(b), make([]byte, 8))
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("zeros 17", func(t *testing.T) {
		var bitlen [19]byte
		bitlen[17] = 1
		b := table20Bits(bitlen, func(w *bitWriter) {
			w.put(0, 1)
			w.put(1, 3)
		})
		table := []byte{9, 9, 9, 9}
		if err := readCodeLengthTable20(bitReaderFrom(b), table); err != nil {
			t.Fatal(err)
		}
		for i, v := range table {
			if v != 0 {
				t.Fatalf("table[%d]=%d, want 0", i, v)
			}
		}
	})

	t.Run("zeros 18 truncated extra", func(t *testing.T) {
		var bitlen [19]byte
		bitlen[18] = 1
		b := table20Bits(bitlen, func(w *bitWriter) { w.put(0, 1) })
		err := readCodeLengthTable20(bitReaderFrom(b), make([]byte, 20))
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("zeros 18", func(t *testing.T) {
		var bitlen [19]byte
		bitlen[18] = 1
		b := table20Bits(bitlen, func(w *bitWriter) {
			w.put(0, 1)
			w.put(0, 7)
		})
		table := bytes.Repeat([]byte{4}, 8)
		if err := readCodeLengthTable20(bitReaderFrom(b), table); err != nil {
			t.Fatal(err)
		}
		for i, v := range table {
			if v != 0 {
				t.Fatalf("table[%d]=%d, want 0", i, v)
			}
		}
	})
}

func TestDecoder20Init(t *testing.T) {
	t.Parallel()
	d := &decoder20{}
	src := memFile{bytes.NewReader(nil)}
	d.init(src, false, 10, decode20Ver)
	if d.br == nil || d.size != 10 {
		t.Fatal("init did not set reader or size")
	}
	if d.version() != decode20Ver {
		t.Fatalf("version %d", d.version())
	}

	d.hdrRead = true
	d.isAudio = true
	d.codeLength[0] = 3
	d.audio = new(audio20Decoder)
	d.audio.chans = 4
	d.audio.curChan = 2
	d.init(src, true, 0, decode20Ver)
	if d.hdrRead || d.isAudio || d.codeLength[0] != 0 {
		t.Fatal("reset did not clear decoder20 state")
	}
	if d.audio.chans != 1 || d.audio.curChan != 0 {
		t.Fatal("reset did not reset audio decoder")
	}

	d.hdrRead = true
	d.init(src, false, 5, decode20Ver)
	if !d.hdrRead {
		t.Fatal("non-reset init cleared hdrRead")
	}
}

func TestDecoder20ReadBlockHeader(t *testing.T) {
	t.Parallel()

	t.Run("truncated flags", func(t *testing.T) {
		d := &decoder20{br: bitReaderFrom(nil)}
		if err := d.readBlockHeader(); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("lz truncated table", func(t *testing.T) {
		d := &decoder20{br: bitReaderFrom([]byte{0}), codeLength: [audioSize * 4]byte{1}}
		err := d.readBlockHeader()
		if err == nil {
			t.Fatal("expected error")
		}
		if !d.hdrRead || d.isAudio || d.lz == nil {
			t.Fatal("lz header path did not record state")
		}
		if d.codeLength[0] != 0 {
			t.Fatal("keep-bit 0 did not clear code lengths")
		}
	})

	t.Run("audio keep table", func(t *testing.T) {
		d := &decoder20{br: bitReaderFrom([]byte{0xc0}), codeLength: [audioSize * 4]byte{2}, audio: new(audio20Decoder)}
		err := d.readBlockHeader()
		if err == nil {
			t.Fatal("expected error")
		}
		if !d.hdrRead || !d.isAudio || d.audio == nil {
			t.Fatal("audio header path did not record state")
		}
		if d.codeLength[0] != 2 {
			t.Fatal("keep-bit 1 cleared code lengths")
		}
	})

	t.Run("lz existing decoder", func(t *testing.T) {
		var bitlen [19]byte
		bitlen[0] = 1
		var w bitWriter
		w.put(0, 1)
		w.put(1, 1)
		for _, v := range bitlen {
			w.put(int(v), 4)
		}
		for i := 0; i < main20Size+offset20Size+length20Size; i++ {
			w.put(0, 1)
		}
		d := &decoder20{br: bitReaderFrom(w.bytes()), lz: new(lz20Decoder)}
		if err := d.readBlockHeader(); err != nil {
			t.Fatal(err)
		}
		if !d.hdrRead || d.isAudio {
			t.Fatal("lz success path did not record state")
		}
	})
}

func TestDecoder20Fill(t *testing.T) {
	t.Parallel()

	t.Run("empty file", func(t *testing.T) {
		err := (&decoder20{size: 0}).fill(newDecodeWindow())
		if !errors.Is(err, io.EOF) {
			t.Fatalf("got %v, want EOF", err)
		}
	})

	t.Run("full window", func(t *testing.T) {
		err := (&decoder20{size: 4}).fill(&decodeReader{})
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("header error", func(t *testing.T) {
		d := &decoder20{size: 4, br: bitReaderFrom(nil)}
		if err := d.fill(newDecodeWindow()); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("lz out of data", func(t *testing.T) {
		d := &decoder20{size: 4, hdrRead: true, lz: new(lz20Decoder)}
		d.lz.br = bitReaderFrom(nil)
		d.lz.mainDecoder.init(oneSym(main20Size, 0, 1))
		err := d.fill(newDecodeWindow())
		if !errors.Is(err, ErrDecoderOutOfData) {
			t.Fatalf("got %v, want ErrDecoderOutOfData", err)
		}
	})

	t.Run("lz end of block", func(t *testing.T) {
		d := &decoder20{size: 4, hdrRead: true, lz: new(lz20Decoder), br: bitReaderFrom(nil)}
		d.lz.br = bitReaderFrom([]byte{0, 0})
		d.lz.mainDecoder.init(oneSym(main20Size, 269, 1))
		err := d.fill(newDecodeWindow())
		if err == nil {
			t.Fatal("expected header error after end of block")
		}
		if d.hdrRead {
			t.Fatal("end of block left hdrRead set")
		}
	})

	t.Run("lz literal then eof", func(t *testing.T) {
		d := &decoder20{size: 1, hdrRead: true, lz: new(lz20Decoder)}
		d.lz.br = bitReaderFrom([]byte{0, 0})
		d.lz.mainDecoder.init(oneSym(main20Size, 'A', 1))
		err := d.fill(newDecodeWindow())
		if !errors.Is(err, io.EOF) {
			t.Fatalf("got %v, want EOF", err)
		}
	})

	t.Run("audio truncated", func(t *testing.T) {
		d := &decoder20{size: 4, hdrRead: true, isAudio: true, audio: new(audio20Decoder)}
		d.audio.br = bitReaderFrom(nil)
		d.audio.chans = 1
		d.audio.decoders[0].init(oneSym(audioSize, 0, 1))
		if err := d.fill(newDecodeWindow()); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestLZ20DecoderInit(t *testing.T) {
	t.Parallel()
	if err := new(lz20Decoder).init(bitReaderFrom(nil), make([]byte, main20Size+offset20Size+length20Size)); err == nil {
		t.Fatal("expected error")
	}

	var bitlen [19]byte
	bitlen[0] = 1
	b := table20Bits(bitlen, func(w *bitWriter) {
		for i := 0; i < main20Size+offset20Size+length20Size; i++ {
			w.put(0, 1)
		}
	})
	d := new(lz20Decoder)
	if err := d.init(bitReaderFrom(b), make([]byte, main20Size+offset20Size+length20Size)); err != nil {
		t.Fatal(err)
	}
}

func TestLZ20DecodeOffset(t *testing.T) {
	t.Parallel()

	t.Run("truncated length extra", func(t *testing.T) {
		d := &lz20Decoder{br: bitReaderFrom(nil)}
		var i int
		for i = range lengthExtraBits {
			if lengthExtraBits[i] > 0 {
				break
			}
		}
		if err := d.decodeOffset(i); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("truncated offset symbol", func(t *testing.T) {
		d := &lz20Decoder{br: bitReaderFrom(nil)}
		if err := d.decodeOffset(0); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("small offset", func(t *testing.T) {
		d := &lz20Decoder{br: bitReaderFrom([]byte{0, 0})}
		d.offsetDecoder.init(oneSym(offset20Size, 0, 1))
		if err := d.decodeOffset(0); err != nil {
			t.Fatal(err)
		}
		if d.length != lengthBase[0]+3 {
			t.Fatalf("length %d", d.length)
		}
		if d.offset[0] != offsetBase[0]+1 {
			t.Fatalf("offset %d", d.offset[0])
		}
	})

	t.Run("length extra and large offset", func(t *testing.T) {
		const osi = 36
		var w bitWriter
		w.put(1, int(lengthExtraBits[8]))
		w.put(0, 1)
		w.put((1<<offsetExtraBits[osi])-1, int(offsetExtraBits[osi]))
		d := &lz20Decoder{br: bitReaderFrom(w.bytes())}
		d.offsetDecoder.init(oneSym(offset20Size, osi, 1))
		if err := d.decodeOffset(8); err != nil {
			t.Fatal(err)
		}
		off := offsetBase[osi] + 1 + (1 << offsetExtraBits[osi]) - 1
		want := lengthBase[8] + 3 + 1
		if off >= 0x2000 {
			want++
		}
		if off >= 0x40000 {
			want++
		}
		if d.length != want {
			t.Fatalf("length %d, want %d (offset %d)", d.length, want, off)
		}
	})

	t.Run("truncated offset extra", func(t *testing.T) {
		d := &lz20Decoder{br: bitReaderFrom([]byte{0, 0})}
		d.offsetDecoder.init(oneSym(offset20Size, 6, 15))
		if err := d.decodeOffset(0); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestLZ20DecodeLength(t *testing.T) {
	t.Parallel()

	t.Run("truncated length symbol", func(t *testing.T) {
		d := &lz20Decoder{br: bitReaderFrom(nil), offset: [4]int{1}}
		if err := d.decodeLength(0); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("truncated extra bits", func(t *testing.T) {
		d := &lz20Decoder{br: bitReaderFrom([]byte{0, 0})}
		d.lengthDecoder.init(oneSym(length20Size, 12, 15))
		if err := d.decodeLength(0); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("offset thresholds", func(t *testing.T) {
		for _, off := range []int{1, 0x101, 0x2000, 0x40000} {
			d := &lz20Decoder{br: bitReaderFrom([]byte{0, 0}), offset: [4]int{off, 9, 8, 7}}
			d.lengthDecoder.init(oneSym(length20Size, 0, 1))
			if err := d.decodeLength(0); err != nil {
				t.Fatal(err)
			}
			want := lengthBase[0] + 2
			if off >= 0x101 {
				want++
			}
			if off >= 0x2000 {
				want++
			}
			if off >= 0x40000 {
				want++
			}
			if d.length != want || d.offset[0] != off {
				t.Fatalf("off=%d length=%d want=%d offset0=%d", off, d.length, want, d.offset[0])
			}
		}
	})

	t.Run("length extra bits", func(t *testing.T) {
		var w bitWriter
		w.put(0, 1)
		w.put(1, int(lengthExtraBits[8]))
		d := &lz20Decoder{br: bitReaderFrom(w.bytes()), offset: [4]int{2}}
		d.lengthDecoder.init(oneSym(length20Size, 8, 1))
		if err := d.decodeLength(0); err != nil {
			t.Fatal(err)
		}
		if d.length != lengthBase[8]+2+1 {
			t.Fatalf("length %d", d.length)
		}
	})
}

func TestLZ20DecodeShortOffset(t *testing.T) {
	t.Parallel()

	t.Run("truncated extra", func(t *testing.T) {
		d := &lz20Decoder{br: bitReaderFrom(nil)}
		if err := d.decodeShortOffset(0); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("extra bits", func(t *testing.T) {
		bits := int(shortOffsetExtraBits[0])
		var w bitWriter
		w.put((1<<bits)-1, bits)
		d := &lz20Decoder{br: bitReaderFrom(w.bytes())}
		if err := d.decodeShortOffset(0); err != nil {
			t.Fatal(err)
		}
		want := shortOffsetBase[0] + 1 + (1 << bits) - 1
		if d.offset[0] != want || d.length != 2 {
			t.Fatalf("offset=%d length=%d", d.offset[0], d.length)
		}
	})
}

func TestLZ20Fill(t *testing.T) {
	t.Parallel()
	window := newDecodeWindow()

	t.Run("truncated symbol", func(t *testing.T) {
		d := &lz20Decoder{br: bitReaderFrom(nil)}
		d.mainDecoder.init(oneSym(main20Size, 0, 1))
		if _, err := d.fill(window, 4); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("literal", func(t *testing.T) {
		d := &lz20Decoder{br: bitReaderFrom([]byte{0, 0})}
		d.mainDecoder.init(oneSym(main20Size, 0x41, 1))
		n, err := d.fill(newDecodeWindow(), 1)
		if err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
	})

	t.Run("end of block", func(t *testing.T) {
		d := &lz20Decoder{br: bitReaderFrom([]byte{0, 0})}
		d.mainDecoder.init(oneSym(main20Size, 269, 1))
		n, err := d.fill(window, 4)
		if n != 0 || !errors.Is(err, errEndOfBlock) {
			t.Fatalf("n=%d err=%v", n, err)
		}
	})

	t.Run("previous match", func(t *testing.T) {
		dr := newDecodeWindow()
		dr.writeByte('a')
		d := &lz20Decoder{br: bitReaderFrom([]byte{0, 0}), length: 1, offset: [4]int{1}}
		d.mainDecoder.init(oneSym(main20Size, 256, 1))
		n, err := d.fill(dr, 1)
		if err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
	})

	t.Run("decode length error", func(t *testing.T) {
		d := &lz20Decoder{br: bitReaderFrom([]byte{0, 0})}
		d.mainDecoder.init(oneSym(main20Size, 257, 15))
		if _, err := d.fill(window, 4); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("decode short offset error", func(t *testing.T) {
		d := &lz20Decoder{br: bitReaderFrom([]byte{0, 0})}
		d.mainDecoder.init(oneSym(main20Size, 261, 15))
		if _, err := d.fill(window, 4); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("decode offset error", func(t *testing.T) {
		d := &lz20Decoder{br: bitReaderFrom([]byte{0, 0})}
		d.mainDecoder.init(oneSym(main20Size, 270, 15))
		if _, err := d.fill(window, 4); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestAudio20ResetAndInit(t *testing.T) {
	t.Parallel()
	d := &audio20Decoder{chans: 3, curChan: 2, chanDelta: 9}
	d.vars[0].lastChar = 7
	d.reset()
	if d.chans != 1 || d.curChan != 0 || d.chanDelta != 0 || d.vars[0].lastChar != 0 {
		t.Fatal("reset left audio state")
	}

	if err := d.init(bitReaderFrom(nil), make([]byte, audioSize*4)); err == nil {
		t.Fatal("expected error")
	}

	d.curChan = 3
	var bitlen [19]byte
	bitlen[0] = 1
	var w bitWriter
	w.put(0, 2)
	for _, v := range bitlen {
		w.put(int(v), 4)
	}
	for i := 0; i < audioSize; i++ {
		w.put(0, 1)
	}
	if err := d.init(bitReaderFrom(w.bytes()), make([]byte, audioSize*4)); err != nil {
		t.Fatal(err)
	}
	if d.chans != 1 || d.curChan != 0 {
		t.Fatalf("chans=%d curChan=%d", d.chans, d.curChan)
	}

	w = bitWriter{}
	w.put(1, 2)
	if err := d.init(bitReaderFrom(w.bytes()), make([]byte, audioSize*4)); err == nil {
		t.Fatal("expected truncated table error")
	}
}

func TestAudio20Decode(t *testing.T) {
	t.Parallel()
	d := &audio20Decoder{chans: 1}
	_ = d.decode(5)
	if d.vars[0].byteCount != 1 {
		t.Fatal("decode did not update state")
	}

	adapt := func(numMin, k int) audio20Decoder {
		a := audio20Decoder{chans: 1}
		v := &a.vars[0]
		v.byteCount = 31
		for i := range v.k {
			v.k[i] = k
		}
		for i := range v.dif {
			v.dif[i] = 100
		}
		v.dif[0] = 50
		if numMin > 0 {
			v.dif[numMin] = 1
		}
		return a
	}

	a := adapt(0, 0)
	a.decode(0)
	if a.vars[0].k[0] != 0 {
		t.Fatalf("numMin 0 changed k: %d", a.vars[0].k[0])
	}

	a = adapt(1, 0)
	a.decode(0)
	if a.vars[0].k[0] != -1 {
		t.Fatalf("even adapt k=%d, want -1", a.vars[0].k[0])
	}

	a = adapt(1, -17)
	a.decode(0)
	if a.vars[0].k[0] != -17 {
		t.Fatalf("k floor moved: %d", a.vars[0].k[0])
	}

	a = adapt(2, 0)
	a.decode(0)
	if a.vars[0].k[0] != 1 {
		t.Fatalf("odd adapt k=%d, want 1", a.vars[0].k[0])
	}

	a = adapt(2, 16)
	a.decode(0)
	if a.vars[0].k[0] != 16 {
		t.Fatalf("k ceiling moved: %d", a.vars[0].k[0])
	}

	a = adapt(3, 4)
	a.decode(0)
	if a.vars[0].k[1] != 3 {
		t.Fatalf("k[1] even adapt %d", a.vars[0].k[1])
	}
}

func TestAudio20Fill(t *testing.T) {
	t.Parallel()

	t.Run("truncated", func(t *testing.T) {
		d := &audio20Decoder{br: bitReaderFrom(nil), chans: 1}
		d.decoders[0].init(oneSym(audioSize, 0, 1))
		if _, err := d.fill(newDecodeWindow(), 4); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("end of block", func(t *testing.T) {
		d := &audio20Decoder{br: bitReaderFrom([]byte{0, 0}), chans: 1}
		d.decoders[0].init(oneSym(audioSize, 256, 1))
		n, err := d.fill(newDecodeWindow(), 4)
		if n != 0 || !errors.Is(err, errEndOfBlock) {
			t.Fatalf("n=%d err=%v", n, err)
		}
	})

	t.Run("sample and wrap channel", func(t *testing.T) {
		d := &audio20Decoder{br: bitReaderFrom([]byte{0, 0}), chans: 2, curChan: 1}
		d.decoders[1].init(oneSym(audioSize, 0, 1))
		n, err := d.fill(newDecodeWindow(), 1)
		if err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		if d.curChan != 0 {
			t.Fatalf("curChan=%d, want wrap to 0", d.curChan)
		}
	})
}

func FuzzReadCodeLengthTable20(f *testing.F) {
	var bitlen [19]byte
	bitlen[0] = 1
	f.Add(table20Bits(bitlen, func(w *bitWriter) { w.put(0, 1) }))
	f.Add([]byte{0})
	f.Fuzz(func(t *testing.T, data []byte) {
		_ = readCodeLengthTable20(bitReaderFrom(data), make([]byte, 32))
	})
}

func FuzzDecoder20Fill(f *testing.F) {
	f.Add([]byte{0}, int64(8), false)
	f.Add([]byte{0xff}, int64(1), true)
	f.Fuzz(func(t *testing.T, data []byte, size int64, reset bool) {
		if size < 0 {
			size = -size
		}
		if size > 64 {
			size %= 65
		}
		d := &decoder20{}
		d.init(memFile{bytes.NewReader(data)}, reset, size, decode20Ver)
		_ = d.fill(newDecodeWindow())
	})
}
