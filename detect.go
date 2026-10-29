package archive

import (
	"bytes"
	"io"
)

const (
	fmtNone = iota
	fmtZip
	fmtRAR
	fmtSevenZip
	fmtTar
	fmtGzip
	fmtXz
	fmtLzma
	fmtZstd
	fmtBzip2
	fmtLz4
)

func detect(head []byte) int {
	switch {
	case bytes.HasPrefix(head, []byte("PK\x03\x04")), bytes.HasPrefix(head, []byte("PK\x05\x06")):
		return fmtZip
	case bytes.HasPrefix(head, []byte("Rar!\x1a\x07\x00")), bytes.HasPrefix(head, []byte("Rar!\x1a\x07\x01\x00")):
		return fmtRAR
	case bytes.HasPrefix(head, []byte("7z\xbc\xaf\x27\x1c")):
		return fmtSevenZip
	}
	return detectStream(head)
}

// detectSource is detect over the first headLen bytes, read through a section
// reader so the caller's offset is untouched.
func detectSource(src io.ReaderAt, baseName string) int {
	var magic [headLen]byte
	n, _ := io.ReadFull(io.NewSectionReader(src, 0, headLen), magic[:])
	kind := detect(magic[:n])
	if kind == fmtNone && emptyTar(magic[:n], baseName) {
		return fmtTar
	}
	return kind
}
