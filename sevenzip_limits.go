package archive

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/ulikunitz/xz/lzma"
)

var errSevenZipHeader = fmt.Errorf("%w: invalid 7z header", ErrMalformed)

const (
	sevenZipMaxFolders  = uint64(DefaultMaxEntries)
	sevenZipMaxCoders   = 64
	sevenZipMaxStreams  = 64
	sevenZipMaxProperty = 16 << 20
	// sevenZipMaxHeader bounds a compressed header once decoded; one that lists
	// DefaultMaxEntries files is a few megabytes.
	sevenZipMaxHeader = 64 << 20
)

// checkSevenZipLimits walks a 7z's headers before the reader parses them, and
// reports whether any folder is encrypted.
func checkSevenZipLimits(r io.ReaderAt, size int64) (encrypted bool, err error) {
	if size < 32 {
		return false, errSevenZipHeader
	}

	var start [32]byte
	if _, err := io.ReadFull(io.NewSectionReader(r, 0, int64(len(start))), start[:]); err != nil {
		return false, errSevenZipHeader
	}
	nextOffset := binary.LittleEndian.Uint64(start[12:20])
	nextSize := binary.LittleEndian.Uint64(start[20:28])
	if nextOffset > uint64(size-32) || nextSize > uint64(size-32)-nextOffset {
		return false, errSevenZipHeader
	}
	offset, err := sevenZipInt64(nextOffset)
	if err != nil {
		return false, err
	}
	length, err := sevenZipInt64(nextSize)
	if err != nil {
		return false, err
	}
	c := sevenZipCursor{r: io.NewSectionReader(r, 32+offset, length), src: r, size: size}
	err = parseSevenZipHeader(&c)
	return c.aes, err
}

// sevenZipInt64 converts a header value already checked against the archive's
// size, so the conversion is proven safe where it happens.
func sevenZipInt64(n uint64) (int64, error) {
	if n > ^uint64(0)>>1 {
		return 0, errSevenZipHeader
	}
	return int64(n), nil
}

type sevenZipCursor struct {
	r io.Reader
	// src and size are the whole archive, for the packed stream an encoded
	// header points at.
	src  io.ReaderAt
	size int64
	// enc records the StreamsInfo of an encoded header while it is parsed.
	enc *sevenZipEncoded
	// aes is set once a folder of the archive's own entries is encrypted.
	aes bool
}

func (c *sevenZipCursor) byte() (byte, error) {
	var b [1]byte
	if _, err := io.ReadFull(c.r, b[:]); err != nil {
		return 0, errSevenZipHeader
	}
	return b[0], nil
}

func (c *sevenZipCursor) uint() (uint64, error) {
	first, err := c.byte()
	if err != nil {
		return 0, err
	}
	mask := byte(0x80)
	extra := 0
	for extra < 8 && first&mask != 0 {
		extra++
		mask >>= 1
	}
	var value uint64
	if extra < 8 {
		// The first byte's remaining bits are the high part, above the extra bytes.
		value = uint64(first&(mask-1)) << (8 * extra)
	}
	for i := range extra {
		b, err := c.byte()
		if err != nil {
			return 0, err
		}
		value |= uint64(b) << (8 * i)
	}
	return value, nil
}

func (c *sevenZipCursor) skip(n uint64) error {
	if n > sevenZipMaxProperty {
		return errSevenZipHeader
	}
	if _, err := io.CopyN(io.Discard, c.r, int64(n)); err != nil {
		return errSevenZipHeader
	}
	return nil
}

func parseSevenZipHeader(c *sevenZipCursor) error {
	id, err := c.byte()
	if err != nil {
		return err
	}
	switch id {
	case 0x17:
		return parseSevenZipEncodedHeader(c)
	case 0x01:
		return parseSevenZipMainHeader(c)
	default:
		return errSevenZipHeader
	}
}

func parseSevenZipMainHeader(c *sevenZipCursor) error {
	for {
		id, err := c.byte()
		if err != nil {
			return err
		}
		switch id {
		case 0:
			return nil
		case 0x02:
			if err := skipSevenZipProperties(c); err != nil {
				return err
			}
		case 0x03, 0x04:
			if err := parseSevenZipStreamsInfo(c); err != nil {
				return err
			}
		case 0x05:
			if err := skipSevenZipFilesInfo(c); err != nil {
				return err
			}
		default:
			return errSevenZipHeader
		}
	}
}

func skipSevenZipProperties(c *sevenZipCursor) error {
	for {
		id, err := c.byte()
		if err != nil {
			return err
		}
		if id == 0 {
			return nil
		}
		size, err := c.uint()
		if err != nil {
			return err
		}
		if err := c.skip(size); err != nil {
			return err
		}
	}
}

func skipSevenZipFilesInfo(c *sevenZipCursor) error {
	n, err := c.uint()
	if err != nil || n > sevenZipMaxFolders {
		return errSevenZipHeader
	}
	return skipSevenZipProperties(c)
}

func parseSevenZipStreamsInfo(c *sevenZipCursor) error {
	for {
		id, err := c.byte()
		if err != nil {
			return err
		}
		switch id {
		case 0:
			return nil
		case 0x06:
			if err := parseSevenZipPackInfo(c); err != nil {
				return err
			}
		case 0x07:
			folders, err := parseSevenZipUnpackInfo(c)
			if err != nil {
				return err
			}
			return parseSevenZipStreamTail(c, folders)
		default:
			return errSevenZipHeader
		}
	}
}

func parseSevenZipPackInfo(c *sevenZipCursor) error {
	packPos, err := c.uint()
	if err != nil {
		return err
	}
	if c.enc != nil {
		c.enc.packPos = packPos
	}
	n, err := c.uint()
	if err != nil || n > sevenZipMaxFolders {
		return errSevenZipHeader
	}
	for {
		id, err := c.byte()
		if err != nil {
			return err
		}
		switch id {
		case 0:
			return nil
		case 0x09:
			for range n {
				size, err := c.uint()
				if err != nil {
					return err
				}
				if c.enc != nil {
					c.enc.packSizes = append(c.enc.packSizes, size)
				}
			}
		case 0x0A:
			if err := parseSevenZipCRCs(c, n); err != nil {
				return err
			}
		default:
			return errSevenZipHeader
		}
	}
}

type sevenZipFolder struct {
	inStreams  uint64
	outStreams uint64
}

func parseSevenZipUnpackInfo(c *sevenZipCursor) ([]sevenZipFolder, error) {
	id, err := c.byte()
	if err != nil || id != 0x0B {
		return nil, errSevenZipHeader
	}
	n, err := c.uint()
	if err != nil || n == 0 || n > sevenZipMaxFolders {
		return nil, errSevenZipHeader
	}
	external, err := c.byte()
	if err != nil || external != 0 {
		return nil, errSevenZipHeader
	}
	folders := make([]sevenZipFolder, 0, n)
	for range n {
		folder, err := parseSevenZipFolder(c)
		if err != nil {
			return nil, err
		}
		folders = append(folders, folder)
	}
	for {
		id, err := c.byte()
		if err != nil {
			return nil, err
		}
		switch id {
		case 0:
			return folders, nil
		case 0x0C:
			for _, folder := range folders {
				for i := uint64(0); i < folder.outStreams; i++ {
					size, err := c.uint()
					if err != nil {
						return nil, err
					}
					if c.enc != nil {
						c.enc.unpackSizes = append(c.enc.unpackSizes, size)
					}
				}
			}
		case 0x0A:
			if err := parseSevenZipCRCs(c, n); err != nil {
				return nil, err
			}
		default:
			return nil, errSevenZipHeader
		}
	}
}

func parseSevenZipFolder(c *sevenZipCursor) (sevenZipFolder, error) {
	n, err := c.uint()
	if err != nil || n == 0 || n > sevenZipMaxCoders {
		return sevenZipFolder{}, errSevenZipHeader
	}
	var folder sevenZipFolder
	// The coders of one folder are all alive while it decodes, so their
	// memory adds up; and 7-Zip writes one compressor per folder, or three
	// behind BCJ2, so a longer chain only multiplies work.
	var memory uint64
	compressors, bcj2 := 0, false
	for range n {
		main, err := c.byte()
		if err != nil {
			return sevenZipFolder{}, err
		}
		idSize := int(main & 0x0F)
		if idSize == 0 {
			return sevenZipFolder{}, errSevenZipHeader
		}
		var method [16]byte
		if _, err := io.ReadFull(c.r, method[:idSize]); err != nil {
			return sevenZipFolder{}, errSevenZipHeader
		}
		inStreams, outStreams := uint64(1), uint64(1)
		if main&0x10 != 0 {
			inStreams, err = c.uint()
			if err != nil {
				return sevenZipFolder{}, err
			}
			outStreams, err = c.uint()
			if err != nil {
				return sevenZipFolder{}, err
			}
			if inStreams == 0 || outStreams == 0 || inStreams > sevenZipMaxStreams || outStreams > sevenZipMaxStreams {
				return sevenZipFolder{}, errSevenZipHeader
			}
		}
		folder.inStreams += inStreams
		folder.outStreams += outStreams
		switch m := method[:idSize]; {
		case bytes.Equal(m, sevenZipAES):
			if c.enc == nil {
				c.aes = true
			}
		case bytes.Equal(m, sevenZipBCJ2):
			bcj2 = true
		case !sevenZipFilter(m):
			compressors++
		}
		if c.enc != nil {
			c.enc.coders = append(c.enc.coders, sevenZipCoder{method: bytes.Clone(method[:idSize])})
		}
		if main&0x20 == 0 {
			continue
		}
		propsSize, err := c.uint()
		if err != nil {
			return sevenZipFolder{}, err
		}
		if propsSize <= 8 {
			props := make([]byte, propsSize)
			if _, err := io.ReadFull(c.r, props); err != nil {
				return sevenZipFolder{}, errSevenZipHeader
			}
			mem, err := checkSevenZipCoder(method[:idSize], propsSize, props)
			if err != nil {
				return sevenZipFolder{}, err
			}
			if memory += mem; memory > uint64(maxDecoderBytes) {
				return sevenZipFolder{}, ErrDecoderMemory
			}
			if c.enc != nil {
				c.enc.coders[len(c.enc.coders)-1].props = props
			}
		} else {
			if err := c.skip(propsSize); err != nil {
				return sevenZipFolder{}, err
			}
			if _, err := checkSevenZipCoder(method[:idSize], propsSize, nil); err != nil {
				return sevenZipFolder{}, err
			}
		}
	}
	if compressors > 1 && (!bcj2 || compressors > 3) {
		return sevenZipFolder{}, errSevenZipHeader
	}
	bindPairs := folder.outStreams - 1
	for range bindPairs {
		if _, err := c.uint(); err != nil {
			return sevenZipFolder{}, err
		}
		if _, err := c.uint(); err != nil {
			return sevenZipFolder{}, err
		}
	}
	if folder.inStreams < bindPairs {
		return sevenZipFolder{}, errSevenZipHeader
	}
	packedStreams := folder.inStreams - bindPairs
	if packedStreams == 0 || packedStreams > sevenZipMaxStreams {
		return sevenZipFolder{}, errSevenZipHeader
	}
	if packedStreams != 1 {
		for range packedStreams {
			if _, err := c.uint(); err != nil {
				return sevenZipFolder{}, err
			}
		}
	}
	return folder, nil
}

// checkSevenZipCoder reports the memory a coder's properties ask for: the LZMA
// and LZMA2 dictionary, or PPMd's model size.
func checkSevenZipCoder(method []byte, propsSize uint64, props []byte) (uint64, error) {
	switch {
	case bytes.Equal(method, []byte{0x21}):
		if propsSize != 1 {
			return 0, errSevenZipHeader
		}
		p := props[0]
		if p > 40 {
			return 0, errSevenZipHeader
		}
		dictCap := uint64(2|p&1) << (uint(p)/2 + 11)
		if dictCap > uint64(maxDecoderBytes) {
			return 0, ErrDecoderMemory
		}
		return dictCap, nil
	case bytes.Equal(method, []byte{0x03, 0x01, 0x01}), bytes.Equal(method, []byte{0x03, 0x04, 0x01}):
		if propsSize != 5 {
			return 0, errSevenZipHeader
		}
		mem := uint64(binary.LittleEndian.Uint32(props[1:]))
		if mem > uint64(maxDecoderBytes) {
			return 0, ErrDecoderMemory
		}
		return mem, nil
	}
	return 0, nil
}

var sevenZipBCJ2 = []byte{0x03, 0x03, 0x01, 0x1B}

// sevenZipFilter reports whether a coder transforms a stream without
// compressing it: Delta, the branch converters and AES.
func sevenZipFilter(method []byte) bool {
	switch string(method) {
	case "\x03", "\x0a", "\x0b",
		"\x03\x03\x01\x03", "\x03\x03\x02\x05", "\x03\x03\x04\x01",
		"\x03\x03\x05\x01", "\x03\x03\x07\x01", "\x03\x03\x08\x05",
		"\x06\xf1\x07\x01":
		return true
	}
	return false
}

func parseSevenZipStreamTail(c *sevenZipCursor, folders []sevenZipFolder) error {
	for {
		id, err := c.byte()
		if err != nil {
			return err
		}
		switch id {
		case 0:
			return nil
		case 0x08:
			if err := skipSevenZipSubStreams(c, folders); err != nil {
				return err
			}
			// SubStreamsInfo ends with its own zero; StreamsInfo's follows it.
			if id, err := c.byte(); err != nil || id != 0 {
				return errSevenZipHeader
			}
			return nil
		default:
			return errSevenZipHeader
		}
	}
}

func skipSevenZipSubStreams(c *sevenZipCursor, folders []sevenZipFolder) error {
	counts := make([]uint64, len(folders))
	for i := range counts {
		counts[i] = 1
	}
	for {
		id, err := c.byte()
		if err != nil {
			return err
		}
		switch id {
		case 0:
			return nil
		case 0x0D:
			var total uint64
			for i := range counts {
				counts[i], err = c.uint()
				if err != nil || counts[i] == 0 {
					return errSevenZipHeader
				}
				total += counts[i]
			}
			if total > sevenZipMaxFolders {
				return errSevenZipHeader
			}
		case 0x09:
			for _, count := range counts {
				for range count - 1 {
					if _, err := c.uint(); err != nil {
						return err
					}
				}
			}
		case 0x0A:
			var total uint64
			for _, count := range counts {
				total += count
			}
			if err := parseSevenZipCRCs(c, total); err != nil {
				return err
			}
		default:
			return errSevenZipHeader
		}
	}
}

func parseSevenZipCRCs(c *sevenZipCursor, n uint64) error {
	allDefined, err := c.byte()
	if err != nil {
		return err
	}
	if allDefined != 0 {
		for range n {
			var crc [4]byte
			if _, err := io.ReadFull(c.r, crc[:]); err != nil {
				return errSevenZipHeader
			}
		}
		return nil
	}
	defined := make([]byte, (n+7)/8)
	for i := range defined {
		var err error
		defined[i], err = c.byte()
		if err != nil {
			return errSevenZipHeader
		}
	}
	for i := range n {
		if defined[i/8]&(1<<uint(i%8)) == 0 {
			continue
		}
		var crc [4]byte
		if _, err := io.ReadFull(c.r, crc[:]); err != nil {
			return errSevenZipHeader
		}
	}
	return nil
}

// sevenZipCoder is one coder of an encoded header's folder.
type sevenZipCoder struct {
	method []byte
	props  []byte
}

// sevenZipEncoded is the StreamsInfo that says how a compressed header is packed.
type sevenZipEncoded struct {
	packPos     uint64
	packSizes   []uint64
	coders      []sevenZipCoder
	unpackSizes []uint64
}

var sevenZipAES = []byte{0x06, 0xF1, 0x07, 0x01}

// parseSevenZipEncodedHeader checks the streams a compressed header is packed
// in, then decodes the header and checks it too. 7-Zip compresses the header by
// default, so its folders and files are only visible once decoded.
func parseSevenZipEncodedHeader(c *sevenZipCursor) error {
	enc := &sevenZipEncoded{}
	c.enc = enc
	err := parseSevenZipStreamsInfo(c)
	c.enc = nil
	if err != nil {
		return err
	}
	for _, coder := range enc.coders {
		// Without a password the reader still decodes a header encrypted with an
		// empty one, which would go unchecked.
		if bytes.Equal(coder.method, sevenZipAES) {
			return ErrEncrypted
		}
	}
	header, err := enc.decode(c.src, c.size)
	if err != nil {
		return err
	}
	hc := sevenZipCursor{r: bytes.NewReader(header)}
	if id, err := hc.byte(); err != nil || id != 0x01 {
		return errSevenZipHeader
	}
	err = parseSevenZipMainHeader(&hc)
	c.aes = hc.aes
	return err
}

// decode unpacks the header with the one coder 7-Zip writes for it, whose
// dictionary the walk has already bounded.
func (e *sevenZipEncoded) decode(src io.ReaderAt, size int64) ([]byte, error) {
	if size < 32 || len(e.coders) != 1 || len(e.packSizes) != 1 || len(e.unpackSizes) != 1 {
		return nil, errSevenZipHeader
	}
	packSize, unpackSize := e.packSizes[0], e.unpackSizes[0]
	if e.packPos > uint64(size-32) || packSize > uint64(size-32)-e.packPos || unpackSize > sevenZipMaxHeader {
		return nil, errSevenZipHeader
	}
	pos, err := sevenZipInt64(e.packPos)
	if err != nil {
		return nil, err
	}
	n, err := sevenZipInt64(packSize)
	if err != nil {
		return nil, err
	}
	packed := io.NewSectionReader(src, 32+pos, n)
	coder := e.coders[0]
	var plain io.Reader
	switch {
	case bytes.Equal(coder.method, []byte{0x00}):
		plain = packed
	case bytes.Equal(coder.method, []byte{0x03, 0x01, 0x01}) && len(coder.props) == 5:
		// The classic .lzma header is the 7z properties followed by the size.
		head := binary.LittleEndian.AppendUint64(bytes.Clone(coder.props), unpackSize)
		lr, err := lzma.NewReader(io.MultiReader(bytes.NewReader(head), packed))
		if err != nil {
			return nil, errSevenZipHeader
		}
		plain = lr
	case bytes.Equal(coder.method, []byte{0x21}) && len(coder.props) == 1:
		p := coder.props[0]
		lr, err := lzma.Reader2Config{DictCap: int(2|p&1) << (int(p)/2 + 11)}.NewReader2(packed)
		if err != nil {
			return nil, errSevenZipHeader
		}
		plain = lr
	default:
		return nil, errSevenZipHeader
	}
	header, err := io.ReadAll(io.LimitReader(plain, int64(unpackSize)+1))
	if err != nil || uint64(len(header)) != unpackSize {
		return nil, errSevenZipHeader
	}
	return header, nil
}
