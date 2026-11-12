package rardecode

import (
	"encoding/binary"
	"hash"
	"math/bits"
)

// BLAKE2sp-256 as specified for RAR5 file hashes (tree blake2s, 8-way).

const (
	blake2spSize        = 32
	blake2sBlockSize    = 64
	blake2spParallelism = 8
)

var blake2sIV = [8]uint32{
	0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
	0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
}

var blake2sSigma = [10][16]byte{
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	{14, 10, 4, 8, 9, 15, 13, 6, 1, 12, 0, 2, 11, 7, 5, 3},
	{11, 8, 12, 0, 5, 2, 15, 13, 10, 14, 3, 6, 7, 1, 9, 4},
	{7, 9, 3, 1, 13, 12, 11, 14, 2, 6, 5, 10, 4, 0, 15, 8},
	{9, 0, 5, 7, 2, 4, 10, 15, 14, 1, 11, 12, 6, 8, 3, 13},
	{2, 12, 6, 10, 0, 11, 8, 3, 4, 13, 7, 5, 15, 14, 1, 9},
	{12, 5, 1, 15, 14, 13, 4, 10, 0, 7, 6, 3, 9, 2, 8, 11},
	{13, 11, 7, 14, 12, 1, 3, 9, 5, 0, 15, 4, 8, 6, 2, 10},
	{6, 15, 14, 9, 11, 3, 0, 8, 12, 2, 13, 7, 1, 4, 10, 5},
	{10, 2, 8, 4, 7, 6, 1, 5, 15, 11, 9, 14, 3, 12, 13, 0},
}

type blake2sState struct {
	h        [8]uint32
	t        [2]uint32
	f        [2]uint32
	buf      [blake2sBlockSize]byte
	buflen   int
	lastNode bool
}

func blake2sG(v *[16]uint32, a, b, c, d int, x, y uint32) {
	v[a] += v[b] + x
	v[d] = bits.RotateLeft32(v[d]^v[a], -16)
	v[c] += v[d]
	v[b] = bits.RotateLeft32(v[b]^v[c], -12)
	v[a] += v[b] + y
	v[d] = bits.RotateLeft32(v[d]^v[a], -8)
	v[c] += v[d]
	v[b] = bits.RotateLeft32(v[b]^v[c], -7)
}

func (s *blake2sState) compress(block []byte) {
	var m [16]uint32
	for i := range m {
		m[i] = binary.LittleEndian.Uint32(block[i*4:])
	}
	var v [16]uint32
	copy(v[:8], s.h[:])
	copy(v[8:], blake2sIV[:])
	v[12] ^= s.t[0]
	v[13] ^= s.t[1]
	v[14] ^= s.f[0]
	v[15] ^= s.f[1]
	for r := range blake2sSigma {
		sig := &blake2sSigma[r]
		blake2sG(&v, 0, 4, 8, 12, m[sig[0]], m[sig[1]])
		blake2sG(&v, 1, 5, 9, 13, m[sig[2]], m[sig[3]])
		blake2sG(&v, 2, 6, 10, 14, m[sig[4]], m[sig[5]])
		blake2sG(&v, 3, 7, 11, 15, m[sig[6]], m[sig[7]])
		blake2sG(&v, 0, 5, 10, 15, m[sig[8]], m[sig[9]])
		blake2sG(&v, 1, 6, 11, 12, m[sig[10]], m[sig[11]])
		blake2sG(&v, 2, 7, 8, 13, m[sig[12]], m[sig[13]])
		blake2sG(&v, 3, 4, 9, 14, m[sig[14]], m[sig[15]])
	}
	for i := range s.h {
		s.h[i] ^= v[i] ^ v[i+8]
	}
}

func (s *blake2sState) initParam(p0, p1, p2, p3 uint32) {
	s.h = blake2sIV
	s.h[0] ^= p0
	s.h[1] ^= p1
	s.h[2] ^= p2
	s.h[3] ^= p3
	s.t = [2]uint32{}
	s.f = [2]uint32{}
	s.buflen = 0
}

func (s *blake2sState) incCounter(n uint32) {
	s.t[0] += n
	if s.t[0] < n {
		s.t[1]++
	}
}

func (s *blake2sState) write(p []byte) {
	if s.buflen > 0 {
		n := copy(s.buf[s.buflen:], p)
		s.buflen += n
		p = p[n:]
		if s.buflen < blake2sBlockSize || len(p) == 0 {
			return
		}
		s.incCounter(blake2sBlockSize)
		s.compress(s.buf[:])
		s.buflen = 0
	}
	for len(p) > blake2sBlockSize {
		s.incCounter(blake2sBlockSize)
		s.compress(p[:blake2sBlockSize])
		p = p[blake2sBlockSize:]
	}
	if len(p) > 0 {
		s.buflen = copy(s.buf[:], p)
	}
}

func (s *blake2sState) final() [blake2spSize]byte {
	s.incCounter(uint32(s.buflen))
	s.f[0] = 0xffffffff
	if s.lastNode {
		s.f[1] = 0xffffffff
	}
	for i := s.buflen; i < blake2sBlockSize; i++ {
		s.buf[i] = 0
	}
	s.compress(s.buf[:])
	var out [blake2spSize]byte
	for i, v := range s.h {
		binary.LittleEndian.PutUint32(out[i*4:], v)
	}
	return out
}

func blake2spLeafParam(nodeOffset uint32) (p0, p1, p2, p3 uint32) {
	// digest_length=32, key_length=0, fanout=8, depth=2
	p0 = 32 | 8<<16 | 2<<24
	p2 = nodeOffset
	// node_depth=0, inner_length=32
	p3 = 32 << 24
	return
}

func blake2spRootParam() (p0, p1, p2, p3 uint32) {
	p0 = 32 | 8<<16 | 2<<24
	p3 = 1<<16 | 32<<24 // node_depth=1, inner_length=32
	return
}

type blake2sp struct {
	leaves [blake2spParallelism]blake2sState
	buf    [blake2spParallelism * blake2sBlockSize]byte
	n      int
}

func newBlake2sp() hash.Hash {
	h := new(blake2sp)
	h.Reset()
	return h
}

func (h *blake2sp) Reset() {
	h.n = 0
	for i := range h.leaves {
		p0, p1, p2, p3 := blake2spLeafParam(uint32(i))
		h.leaves[i].initParam(p0, p1, p2, p3)
		if i == blake2spParallelism-1 {
			h.leaves[i].lastNode = true
		}
	}
}

func (h *blake2sp) Size() int      { return blake2spSize }
func (h *blake2sp) BlockSize() int { return blake2sBlockSize }

func (h *blake2sp) flush() {
	for i := range h.leaves {
		h.leaves[i].write(h.buf[i*blake2sBlockSize : (i+1)*blake2sBlockSize])
	}
	h.n = 0
}

func (h *blake2sp) Write(p []byte) (int, error) {
	written := len(p)
	if h.n > 0 {
		n := copy(h.buf[h.n:], p)
		h.n += n
		p = p[n:]
		if h.n == len(h.buf) {
			h.flush()
		}
	}
	for len(p) >= len(h.buf) {
		for i := range h.leaves {
			off := i * blake2sBlockSize
			h.leaves[i].write(p[off : off+blake2sBlockSize])
		}
		p = p[len(h.buf):]
	}
	if len(p) > 0 {
		h.n = copy(h.buf[:], p)
	}
	return written, nil
}

func (h *blake2sp) Sum(b []byte) []byte {
	c := *h
	for i := range c.leaves {
		inlen := 0
		if c.n > i*blake2sBlockSize {
			inlen = c.n - i*blake2sBlockSize
			if inlen > blake2sBlockSize {
				inlen = blake2sBlockSize
			}
			c.leaves[i].write(c.buf[i*blake2sBlockSize : i*blake2sBlockSize+inlen])
		}
	}
	var root blake2sState
	p0, p1, p2, p3 := blake2spRootParam()
	root.initParam(p0, p1, p2, p3)
	root.lastNode = true
	for i := range c.leaves {
		sum := c.leaves[i].final()
		root.write(sum[:])
	}
	out := root.final()
	return append(b, out[:]...)
}
