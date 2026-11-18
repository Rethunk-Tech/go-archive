package rardecode

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func TestNewReaderTestdata(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"../../testdata/real.rar",
		"../../testdata/solid-rar4.rar",
		"../../testdata/solid-rar5.rar",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			r, err := NewReader(bytes.NewReader(b), MaxDictionarySize(1<<20), MaxReadFileSize(1<<20))
			if err != nil {
				t.Fatal(err)
			}
			for {
				if _, err := r.Next(); err != nil {
					if err != io.EOF {
						return
					}
					return
				}
				_, _ = io.CopyN(io.Discard, r, 1<<20)
			}
		})
	}
}

func FuzzNewReader(f *testing.F) {
	for _, name := range []string{
		"../../testdata/real.rar",
		"../../testdata/solid-rar4.rar",
		"../../testdata/solid-rar5.rar",
	} {
		b, err := os.ReadFile(name)
		if err == nil {
			f.Add(b)
		}
	}
	f.Add([]byte(nil))
	f.Add([]byte("Rar!\x1a\x07\x00"))
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := NewReader(bytes.NewReader(data), MaxDictionarySize(1<<20), MaxReadFileSize(1<<20))
		if err != nil {
			return
		}
		for i := 0; i < 8; i++ {
			if _, err := r.Next(); err != nil {
				return
			}
			_, _ = io.CopyN(io.Discard, r, 1<<16)
		}
	})
}
