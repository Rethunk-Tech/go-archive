package archive

import (
	"archive/tar"
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sevenZipNum encodes v as a 7z number: the extra bytes are little-endian and
// low-order.
func sevenZipNum(v uint64) []byte {
	switch {
	case v < 0x80:
		return []byte{byte(v)}
	case v < 0x4000:
		return []byte{0x80 | byte(v>>8&0x3F), byte(v & 0xFF)}
	}
	return []byte{0xC0 | byte(v>>16&0x1F), byte(v & 0xFF), byte(v >> 8 & 0xFF)}
}

// sevenZipWith is a 7z holding packed bytes and the plain header hdr after them.
func sevenZipWith(hdr, packed []byte) []byte {
	out := append([]byte("7z\xbc\xaf\x27\x1c\x00\x04"), make([]byte, 24)...)
	out = append(out, packed...)
	binary.LittleEndian.PutUint64(out[12:20], uint64(len(packed)))
	binary.LittleEndian.PutUint64(out[20:28], uint64(len(hdr)))
	binary.LittleEndian.PutUint32(out[28:32], crc32.ChecksumIEEE(hdr))
	binary.LittleEndian.PutUint32(out[8:12], crc32.ChecksumIEEE(out[12:32]))
	return append(out, hdr...)
}

// encodeSevenZipHeader moves a 7z's plain header into a Copy-coded packed stream
// and points an encoded header at it, the shape 7-Zip writes by default.
func encodeSevenZipHeader(b []byte) []byte {
	off := binary.LittleEndian.Uint64(b[12:20])
	size := binary.LittleEndian.Uint64(b[20:28])
	hdr := b[32+off : 32+off+size]
	body := append([]byte{}, b[32:32+off]...)
	packPos := uint64(len(body))
	body = append(body, hdr...)
	enc := []byte{0x17, 0x06}
	enc = append(enc, sevenZipNum(packPos)...)
	enc = append(enc, 0x01, 0x09)
	enc = append(enc, sevenZipNum(uint64(len(hdr)))...)
	enc = append(enc, 0x00, 0x07, 0x0B, 0x01, 0x00, 0x01, 0x01, 0x00, 0x0C)
	enc = append(enc, sevenZipNum(uint64(len(hdr)))...)
	enc = append(enc, 0x00, 0x00)
	out := append([]byte{}, b[:32]...)
	out = append(out, body...)
	binary.LittleEndian.PutUint64(out[12:20], uint64(len(body)))
	binary.LittleEndian.PutUint64(out[20:28], uint64(len(enc)))
	binary.LittleEndian.PutUint32(out[28:32], crc32.ChecksumIEEE(enc))
	binary.LittleEndian.PutUint32(out[8:12], crc32.ChecksumIEEE(out[12:32]))
	return append(out, enc...)
}

// A header's one packed stream of a byte, one Copy folder of a byte.
var sevenZipOneByteStreams = []byte{
	0x06, 0x00, 0x01, 0x09, 0x01, 0x00,
	0x07, 0x0B, 0x01, 0x00, 0x01, 0x01, 0x00, 0x0C, 0x01, 0x00,
}

func TestSevenZipNumbersAreLittleEndian(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   []byte
		want uint64
	}{
		{[]byte{0x7F}, 0x7F},
		{[]byte{0x81, 0x02}, 0x0102},
		{[]byte{0xC0, 0x34, 0x12}, 0x1234},
		{[]byte{0xE1, 0x56, 0x34, 0x12}, 0x01123456},
		{[]byte{0xFF, 1, 0, 0, 0, 0, 0, 0, 0x80}, 0x8000000000000001},
	} {
		c := sevenZipCursor{r: bytes.NewReader(tc.in)}
		if got, err := c.uint(); err != nil || got != tc.want {
			t.Errorf("% x = %#x, %v; want %#x", tc.in, got, err, tc.want)
		}
	}
}

// 7-Zip compresses the header, so the decoder memory it names is only visible
// once the header is decoded.
func TestSevenZipEncodedHeaderIsChecked(t *testing.T) {
	t.Parallel()
	plain, err := os.ReadFile(buildSevenZipMemoryBomb(t, false))
	if err != nil {
		t.Fatal(err)
	}
	src := writeTemp(t, "encoded.7z", encodeSevenZipHeader(plain))
	_, err = extractDir(t, src, Options{})
	if err == nil || !strings.Contains(err.Error(), "decoder memory exceeds the extraction cap") {
		t.Fatalf("got %v, want the decoder memory refusal", err)
	}
	if _, err := declaredSize(t, src); err == nil {
		t.Fatal("DeclaredSize read past the header limits")
	}
}

// SubStreamsInfo ends with its own zero and StreamsInfo with another; reading
// only the first left the files that follow unchecked.
func TestSevenZipFilesInfoAfterSubStreamsIsChecked(t *testing.T) {
	t.Parallel()
	hdr := append([]byte{0x01, 0x04}, sevenZipOneByteStreams...)
	hdr = append(hdr, 0x08, 0x00, 0x00)
	hdr = append(hdr, 0x05, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x00, 0x00)
	b := sevenZipWith(hdr, []byte("x"))
	if _, err := checkSevenZipLimits(bytes.NewReader(b), int64(len(b))); !errors.Is(err, ErrMalformed) {
		t.Fatalf("got %v, want ErrMalformed for 2^64-1 files", err)
	}
}

// A substream count the reader would allocate for, written in more than two bytes.
func TestSevenZipLargeSubstreamCountIsRefused(t *testing.T) {
	t.Parallel()
	hdr := append([]byte{0x01, 0x04}, sevenZipOneByteStreams...)
	hdr = append(hdr, 0x08, 0x0D, 0xF0, 0x00, 0x00, 0x00, 0x10, 0x00, 0x00, 0x00)
	_, err := extractDir(t, writeTemp(t, "sub.7z", sevenZipWith(hdr, []byte("x"))), Options{})
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("got %v, want ErrMalformed", err)
	}
}

// A zstd frame names its own window, past what the header walk sees.
func TestSevenZipZstdWindowIsBounded(t *testing.T) {
	t.Parallel()
	frame := []byte{0x28, 0xB5, 0x2F, 0xFD, 0x00, 0x98, 0x09, 0x00, 0x00, 'x'} // 512 MiB window
	hdr := []byte{
		0x01, 0x04,
		0x06, 0x00, 0x01, 0x09, 0x0A, 0x00, // the frame's 10 bytes
		0x07, 0x0B, 0x01, 0x00, 0x01, 0x04, 0x04, 0xF7, 0x11, 0x01, 0x0C, 0x01, 0x00,
		0x00,
		0x05, 0x01, 0x11, 0x05, 0x00, 'f', 0, 0, 0, 0x00,
		0x00,
	}
	l := openListerPath(t, writeTemp(t, "zstd.7z", sevenZipWith(hdr, frame)), ListerOptions{})
	if _, err := l.Next(); err != nil {
		t.Fatal(err)
	}
	rc, err := l.Open()
	if err == nil {
		_, err = io.ReadAll(rc)
	}
	if err == nil {
		t.Fatal("a 512 MiB zstd window decoded")
	}
}

// The reader reports a 7z written without digests as CRC zero.
func TestSevenZipWithoutDigestsExtracts(t *testing.T) {
	t.Parallel()
	hdr := append([]byte{0x01, 0x04}, sevenZipOneByteStreams...)
	hdr = append(hdr, 0x00, 0x05, 0x01, 0x11, 0x05, 0x00, 'f', 0, 0, 0, 0x00, 0x00)
	dest, err := extractDir(t, writeTemp(t, "nocrc.7z", sevenZipWith(hdr, []byte("x"))), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dest, "f")); got != "x" {
		t.Fatalf("f = %q", got)
	}
}

// A solid 7z decodes every earlier entry to reach a later one, read or not, so
// an entry Extract skips still counts against the ratio.
func TestSkippedSolidEntryCountsAgainstTheRatio(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(filepath.Join("testdata", "solid-zeros.7z"))
	if err != nil {
		t.Fatal(err)
	}
	off := binary.LittleEndian.Uint64(b[12:20])
	hdr := b[32+off:]
	i := bytes.Index(hdr, []byte{'a', 0, 0, 0})
	if i < 0 {
		t.Fatal("fixture header names no entry a")
	}
	hdr[i] = '.'
	binary.LittleEndian.PutUint32(b[28:32], crc32.ChecksumIEEE(hdr))
	binary.LittleEndian.PutUint32(b[8:12], crc32.ChecksumIEEE(b[12:32]))
	_, err = extractDir(t, writeTemp(t, "dot.7z", b), Options{MaxRatio: 1, RatioFloor: 1 << 20})
	wantReason(t, err, ErrRatio, ".")
}

func TestSevenZipOverTheEntryCapWritesNothing(t *testing.T) {
	t.Parallel()
	dest, err := extractDir(t, filepath.Join("testdata", "valid.7z"), Options{MaxEntries: 1})
	wantReason(t, err, ErrTooManyEntries, "")
	if ents, _ := os.ReadDir(dest); len(ents) != 0 {
		t.Fatalf("wrote %d entries", len(ents))
	}
}

// A folder's coders are alive together, so their dictionaries add up, and 7-Zip
// never chains two compressors outside BCJ2.
func TestSevenZipFolderRules(t *testing.T) {
	t.Parallel()
	lzma := func(dict uint32) []byte {
		props := binary.LittleEndian.AppendUint32([]byte{0x5D}, dict)
		return append([]byte{0x23, 0x03, 0x01, 0x01, 0x05}, props...)
	}
	folder := func(coders ...[]byte) error {
		b := []byte{byte(len(coders) & 0x7F)}
		for _, c := range coders {
			b = append(b, c...)
		}
		for range len(coders) - 1 {
			b = append(b, 0x00, 0x00) // bind pairs
		}
		_, err := parseSevenZipFolder(&sevenZipCursor{r: bytes.NewReader(b)})
		return err
	}
	if err := folder(lzma(1 << 20)); err != nil {
		t.Fatalf("one LZMA coder: %v", err)
	}
	if err := folder(lzma(1<<20), lzma(1<<20)); !errors.Is(err, ErrMalformed) {
		t.Fatalf("two chained compressors: %v, want ErrMalformed", err)
	}
	if mem, err := checkSevenZipCoder([]byte{0x03, 0x01, 0x01}, 5, []byte{0x5D, 0, 0, 0, 0x10}); err != nil || mem != 256<<20 {
		t.Fatalf("a 256 MiB dictionary reports %d, %v", mem, err)
	}
	if _, err := checkSevenZipCoder([]byte{0x21}, 1, []byte{40}); !errors.Is(err, ErrDecoderMemory) {
		t.Fatalf("a 4 GiB LZMA2 dictionary: %v, want ErrDecoderMemory", err)
	}
}

// Without a password an encrypted entry cannot be read, and trying costs a key
// derivation, so the Lister lists it and refuses its bytes.
func TestListerRefusesToOpenAnEncryptedSevenZipEntry(t *testing.T) {
	t.Parallel()
	l := openListerPath(t, filepath.Join("testdata", "encrypted.7z"), ListerOptions{})
	for {
		e, err := l.Next()
		if err != nil {
			t.Fatalf("no file entry: %v", err)
		}
		if e.IsDir {
			continue
		}
		if _, err := l.Open(); !errors.Is(err, ErrEncrypted) {
			t.Fatalf("Open = %v, want ErrEncrypted", err)
		}
		return
	}
}

func TestGNUSparseTarEntryIsARegularFile(t *testing.T) {
	t.Parallel()
	if m := tarMode(tar.TypeGNUSparse); m != 0 {
		t.Fatalf("tarMode(GNU sparse) = %v, want a regular file", m)
	}
}

// 7-Zip's BCJ2 folder joins four coders and three compressors through bind
// pairs, the one chain the folder rules allow past a single compressor.
func TestSevenZipBCJ2Extracts(t *testing.T) {
	t.Parallel()
	dest, err := extractDir(t, filepath.Join("testdata", "bcj2.7z"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dest, "prog.bin")); err != nil || info.Size() != 40000 {
		t.Fatalf("prog.bin: %v, %v", info, err)
	}
}

// Leaving a solid folder partway reads its last entry to the end, so the walk
// goes on to report the rest and nothing fails along the way.
func TestListerLeavesASolidFolderReadPartway(t *testing.T) {
	t.Parallel()
	l := openListerPath(t, filepath.Join("testdata", "solid-zeros.7z"), ListerOptions{})
	read, seen := false, 0
	for {
		e, err := l.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		seen++
		if e.IsDir || read {
			continue
		}
		rc, err := l.Open()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(rc, make([]byte, 1)); err != nil {
			t.Fatal(err)
		}
		read = true
	}
	if !read || seen != 2 {
		t.Fatalf("read %v across %d entries", read, seen)
	}
}
