package rardecode

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"io"
	"io/fs"
	"testing"
)

type chunkReader struct {
	r *bytes.Reader
	n int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.n > 0 && len(p) > c.n {
		p = p[:c.n]
	}
	return c.r.Read(p)
}

func (c *chunkReader) ReadByte() (byte, error) {
	return c.r.ReadByte()
}

type stubArchiveFile struct {
	byteReader
}

func (s stubArchiveFile) WriteTo(w io.Writer) (int64, error) {
	return io.Copy(w, s.byteReader)
}

func (s stubArchiveFile) writeToN(w io.Writer, n int64) (int64, error) {
	return io.CopyN(w, s.byteReader, n)
}

func (s stubArchiveFile) currFile() *fileBlockHeader { return nil }

func (s stubArchiveFile) nextFile() (*fileBlockList, error) { return nil, io.EOF }

func (s stubArchiveFile) newArchiveFile(*fileBlockList) (archiveFile, error) {
	return s, nil
}

func (s stubArchiveFile) Stat() (fs.FileInfo, error) { return nil, fs.ErrNotExist }

type shortWriter struct {
	n   int
	err error
}

func (w *shortWriter) Write(p []byte) (int, error) {
	if w.n <= 0 {
		return 0, w.err
	}
	n := min(len(p), w.n)
	w.n -= n
	if w.n == 0 {
		return n, w.err
	}
	return n, nil
}

func aesCBC(t *testing.T, key, iv, plain []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain)%aes.BlockSize != 0 {
		t.Fatal("plaintext not block aligned")
	}
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, plain)
	return out
}

func TestCipherBlockReader(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0x11}, 16)
	iv := bytes.Repeat([]byte{0x22}, 16)
	plain := bytes.Repeat([]byte("0123456789abcdef"), 3)
	ct := aesCBC(t, key, iv, plain)

	t.Run("invalid_key", func(t *testing.T) {
		t.Parallel()
		_, err := newAesDecryptReader(bytes.NewReader(ct), key[:15], iv)
		if err == nil {
			t.Fatal("expected error")
		}
		_, err = newAesDecryptFileReader(stubArchiveFile{bytes.NewReader(ct)}, key[:15], iv)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("read_paths", func(t *testing.T) {
		t.Parallel()
		r, err := newAesDecryptReader(&chunkReader{r: bytes.NewReader(ct), n: 20}, key, iv)
		if err != nil {
			t.Fatal(err)
		}
		small := make([]byte, 4)
		n, err := r.Read(small)
		if err != nil || n != 4 || !bytes.Equal(small, plain[:4]) {
			t.Fatalf("small read n=%d err=%v got=%q", n, err, small)
		}
		n, err = r.Read(small)
		if err != nil || n != 4 || !bytes.Equal(small, plain[4:8]) {
			t.Fatalf("outbuf read n=%d err=%v got=%q", n, err, small)
		}
		rest := make([]byte, 64)
		n, err = r.Read(rest)
		if err != nil {
			t.Fatal(err)
		}
		got := append([]byte{}, plain[:8]...)
		got = append(got, rest[:n]...)
		if !bytes.HasPrefix(plain, got) {
			t.Fatalf("got %q want prefix of %q", got, plain)
		}
		empty, err := newAesDecryptReader(bytes.NewReader(nil), key, iv)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := empty.Read(make([]byte, 4)); !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("empty read: %v", err)
		}
		if _, err := empty.ReadByte(); !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("empty ReadByte: %v", err)
		}
	})

	t.Run("read_byte_and_writeto", func(t *testing.T) {
		t.Parallel()
		r, err := newAesDecryptReader(bytes.NewReader(ct), key, iv)
		if err != nil {
			t.Fatal(err)
		}
		b, err := r.ReadByte()
		if err != nil || b != plain[0] {
			t.Fatalf("ReadByte=%q err=%v", b, err)
		}
		var buf bytes.Buffer
		n, err := r.WriteTo(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if int(n) != len(plain)-1 || !bytes.Equal(buf.Bytes(), plain[1:]) {
			t.Fatalf("WriteTo n=%d got=%q", n, buf.Bytes())
		}
	})

	t.Run("writeton", func(t *testing.T) {
		t.Parallel()
		r, err := newAesDecryptReader(bytes.NewReader(ct), key, iv)
		if err != nil {
			t.Fatal(err)
		}
		n, err := r.writeToN(io.Discard, 0)
		if n != 0 || err != nil {
			t.Fatalf("zero: n=%d err=%v", n, err)
		}
		var buf bytes.Buffer
		n, err = r.writeToN(&buf, 10)
		if err != nil || n != 10 || !bytes.Equal(buf.Bytes(), plain[:10]) {
			t.Fatalf("limited n=%d err=%v got=%q", n, err, buf.Bytes())
		}
		w := &shortWriter{n: 3, err: io.ErrClosedPipe}
		_, err = r.writeToN(w, -1)
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("short write: %v", err)
		}
	})

	t.Run("aligned_inbuf", func(t *testing.T) {
		t.Parallel()
		r, err := newAesDecryptReader(&chunkReader{r: bytes.NewReader(ct), n: 20}, key, iv)
		if err != nil {
			t.Fatal(err)
		}
		p := make([]byte, 32)
		n, err := r.Read(p)
		if err != nil || n != 16 || !bytes.Equal(p[:n], plain[:16]) {
			t.Fatalf("first n=%d err=%v got=%q", n, err, p[:n])
		}
		n, err = r.Read(p)
		if err != nil || n < 16 || !bytes.Equal(p[:16], plain[16:32]) {
			t.Fatalf("inbuf n=%d err=%v got=%q", n, err, p[:n])
		}
		short, err := newAesDecryptReader(bytes.NewReader(ct[:8]), key, iv)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := short.Read(make([]byte, 32)); !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("short aligned: %v", err)
		}
	})

	t.Run("file_reader", func(t *testing.T) {
		t.Parallel()
		fr, err := newAesDecryptFileReader(stubArchiveFile{bytes.NewReader(ct)}, key, iv)
		if err != nil {
			t.Fatal(err)
		}
		b, err := fr.ReadByte()
		if err != nil || b != plain[0] {
			t.Fatalf("ReadByte=%q err=%v", b, err)
		}
		p := make([]byte, 7)
		n, err := fr.Read(p)
		if err != nil || n != 7 || !bytes.Equal(p, plain[1:8]) {
			t.Fatalf("Read n=%d err=%v got=%q", n, err, p)
		}
		var buf bytes.Buffer
		wn, err := fr.writeToN(&buf, 5)
		if err != nil || wn != 5 || !bytes.Equal(buf.Bytes(), plain[8:13]) {
			t.Fatalf("writeToN n=%d err=%v got=%q", wn, err, buf.Bytes())
		}
		buf.Reset()
		_, err = fr.WriteTo(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf.Bytes(), plain[13:]) {
			t.Fatalf("WriteTo got=%q", buf.Bytes())
		}
	})
}
