package rardecode

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"
)

func fileBlocks(h *fileBlockHeader) *fileBlockList {
	return &fileBlockList{blocks: []*fileBlockHeader{h}}
}

func testHeader(name string) *fileBlockHeader {
	mod := time.Unix(1_700_000_000, 0).UTC()
	return &fileBlockHeader{FileHeader: FileHeader{
		Name:             name,
		UnPackedSize:     4,
		ModificationTime: mod,
		Attributes:       0o644,
	}}
}

func TestReadFileLimitReader(t *testing.T) {
	t.Parallel()
	r := newReadFileLimitReader(bytes.NewReader([]byte("abcd")), -1)
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, ErrFileTooLargeForReadFile) {
		t.Fatalf("neg: %v", err)
	}
	r = newReadFileLimitReader(bytes.NewReader([]byte("abcd")), 0)
	if _, err := r.Read(make([]byte, 4)); !errors.Is(err, ErrFileTooLargeForReadFile) {
		t.Fatalf("zero: %v", err)
	}
	r = newReadFileLimitReader(bytes.NewReader([]byte("abcd")), 2)
	got := make([]byte, 8)
	n, err := r.Read(got)
	if err != nil || n != 2 || string(got[:n]) != "ab" {
		t.Fatalf("clip n=%d err=%v got=%q", n, err, got[:n])
	}
}

func TestFSInfoTypes(t *testing.T) {
	t.Parallel()
	h := testHeader("dir/a.txt")
	h.IsDir = true
	fi := fileInfo{h: h}
	if fi.Name() != "a.txt" || fi.Size() != 4 || !fi.IsDir() || fi.Sys() != nil || fi.ModTime().IsZero() {
		t.Fatalf("fileInfo %+v", fi)
	}
	_ = fi.Mode()
	de := dirEntry{h: h}
	if de.Name() != "a.txt" || !de.IsDir() {
		t.Fatal("dirEntry")
	}
	info, err := de.Info()
	if err != nil || info.Name() != "a.txt" {
		t.Fatal(err)
	}
	_ = de.Type()

	di := newDummyDirInfo("sub/dir")
	if di.Name() != "dir" || di.Size() != 0 || !di.IsDir() || di.Sys() != nil || !di.ModTime().IsZero() {
		t.Fatalf("dummyDirInfo %+v", di)
	}
	_ = di.Mode()
	dd := newDummyDirEntry("sub/dir")
	if dd.Name() != "dir" || !dd.IsDir() || dd.Type() != fs.ModeDir || dd.Sys() != nil {
		t.Fatal("dummyDirEntry")
	}
	info, err = dd.Info()
	if err != nil || info.Name() != "dir" {
		t.Fatal(err)
	}

	df := &dirFile{
		name:  ".",
		info:  di,
		files: []fs.DirEntry{dd, de},
	}
	if n, err := df.Read(nil); n != 0 || err != io.EOF {
		t.Fatalf("Read %d %v", n, err)
	}
	if _, err := df.ReadByte(); err != io.EOF {
		t.Fatal(err)
	}
	if st, err := df.Stat(); err != nil || st.Name() != "dir" {
		t.Fatal(err)
	}
	if err := df.Close(); err != nil {
		t.Fatal(err)
	}
	all, err := df.ReadDir(-1)
	if err != nil || len(all) != 2 {
		t.Fatalf("ReadDir all %d %v", len(all), err)
	}
	got, err := df.ReadDir(1)
	if err != nil || len(got) != 2 {
		t.Fatalf("ReadDir n %d %v", len(got), err)
	}
}

func TestFSNode(t *testing.T) {
	t.Parallel()
	empty := &fsNode{name: "empty"}
	if !empty.isDir() || empty.firstBlock() != nil {
		t.Fatal("empty node")
	}
	if empty.fileInfo().Name() != "empty" || empty.dirEntry().Name() != "empty" {
		t.Fatal("dummy fallback")
	}
	a := &fsNode{name: "z.txt", blocks: fileBlocks(testHeader("z.txt"))}
	b := &fsNode{name: "a.txt", blocks: fileBlocks(testHeader("a.txt"))}
	n := &fsNode{name: ".", files: []*fsNode{a, b}}
	list := n.dirEntryList()
	if len(list) != 2 || list[0].Name() != "a.txt" || list[1].Name() != "z.txt" {
		t.Fatalf("sort %v %v", list[0].Name(), list[1].Name())
	}
	if a.isDir() {
		t.Fatal("file node")
	}
}

func TestRarFSMethods(t *testing.T) {
	t.Parallel()
	fileH := testHeader("dir/a.txt")
	dirH := testHeader("dir")
	dirH.IsDir = true
	linkH := testHeader("link")
	linkH.LinkType = LinkTypeUnixSymlink
	linkH.LinkTarget = "dir/a.txt"
	fileNode := &fsNode{name: "a.txt", blocks: fileBlocks(fileH)}
	dirNode := &fsNode{name: "dir", blocks: fileBlocks(dirH), files: []*fsNode{fileNode}}
	linkNode := &fsNode{name: "link", blocks: fileBlocks(linkH)}
	dummy := &fsNode{name: "ghost"}
	rfs := &RarFS{
		vm: &volumeManager{files: []string{"x.rar"}, opt: getOptions([]Option{FileSystem(fstest.MapFS{})})},
		ftree: map[string]*fsNode{
			".":         {name: ".", files: []*fsNode{dirNode, linkNode, dummy}},
			"dir":       dirNode,
			"dir/a.txt": fileNode,
			"link":      linkNode,
			"ghost":     dummy,
		},
	}

	if _, err := rfs.Open(".."); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("open invalid %v", err)
	}
	if _, err := rfs.Open("nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("open missing %v", err)
	}
	d, err := rfs.Open("dir")
	if err != nil {
		t.Fatal(err)
	}
	df := d.(*dirFile)
	if _, err := df.Stat(); err != nil {
		t.Fatal(err)
	}
	g, err := rfs.Open("ghost")
	if err != nil {
		t.Fatal(err)
	}
	if st, err := g.Stat(); err != nil || !st.IsDir() {
		t.Fatal(err)
	}
	if _, err := rfs.Open("dir/a.txt"); err == nil {
		t.Fatal("missing volume should fail opening file")
	}

	if _, err := rfs.ReadDir(".."); !errors.Is(err, fs.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := rfs.ReadDir("nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	if _, err := rfs.ReadDir("dir/a.txt"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatal(err)
	}
	ents, err := rfs.ReadDir("dir")
	if err != nil || len(ents) != 1 || ents[0].Name() != "a.txt" {
		t.Fatalf("ReadDir %v %v", ents, err)
	}

	if _, err := rfs.ReadFile(".."); !errors.Is(err, fs.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := rfs.ReadFile("nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	if b, err := rfs.ReadFile("dir"); err != nil || len(b) != 0 {
		t.Fatalf("dir bytes %v %v", b, err)
	}
	if _, err := rfs.ReadFile("ghost"); err != nil {
		t.Fatal(err)
	}
	if _, err := rfs.ReadFile("dir/a.txt"); err == nil {
		t.Fatal("missing volume ReadFile")
	}

	if _, err := rfs.ReadLink(".."); !errors.Is(err, fs.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := rfs.ReadLink("nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	if _, err := rfs.ReadLink("dir/a.txt"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatal(err)
	}
	tgt, err := rfs.ReadLink("link")
	if err != nil || tgt != "dir/a.txt" {
		t.Fatalf("link %q %v", tgt, err)
	}

	if _, err := rfs.Stat(".."); !errors.Is(err, fs.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := rfs.Stat("nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	st, err := rfs.Stat("dir/a.txt")
	if err != nil || st.Name() != "a.txt" {
		t.Fatal(err)
	}
	st, err = rfs.Stat("ghost")
	if err != nil || !st.IsDir() {
		t.Fatal(err)
	}

	sub, err := rfs.Sub(".")
	if err != nil || sub != rfs {
		t.Fatal("sub self")
	}
	if _, err := rfs.Sub(".."); !errors.Is(err, fs.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := rfs.Sub("nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	if _, err := rfs.Sub("dir/a.txt"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatal(err)
	}
	sub, err = rfs.Sub("dir")
	if err != nil {
		t.Fatal(err)
	}
	ents, err = sub.(fs.ReadDirFS).ReadDir(".")
	if err != nil || len(ents) != 1 {
		t.Fatalf("sub dir %v %v", ents, err)
	}
}

func TestOpenFSRealArchive(t *testing.T) {
	t.Parallel()
	path := testdataFile(t, "real.rar")
	if _, err := OpenFS(filepath.Join("..", "..", "testdata", "missing.rar")); err == nil {
		t.Fatal("missing archive")
	}
	rfs, err := OpenFS(path, OpenFSCheck, MaxReadFileSize(1<<20))
	if err != nil {
		t.Fatal(err)
	}
	st, err := rfs.Stat("asd.go")
	if err != nil || st.IsDir() {
		t.Fatal(err)
	}
	b, err := rfs.ReadFile("asd.go")
	if err != nil || len(b) == 0 {
		t.Fatalf("ReadFile %d %v", len(b), err)
	}
	f, err := rfs.Open("asd.go")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	ents, err := rfs.ReadDir(".")
	if err != nil || len(ents) == 0 {
		t.Fatal(err)
	}

	node := rfs.ftree["asd.go"]
	h := node.firstBlock()
	h.UnKnownSize = true
	got, err := rfs.ReadFile("asd.go")
	if err != nil || len(got) == 0 {
		t.Fatalf("unknown size %d %v", len(got), err)
	}
	h.UnKnownSize = true
	rfs.vm.opt.maxReadFileSize = 1
	if _, err := rfs.ReadFile("asd.go"); !errors.Is(err, ErrFileTooLargeForReadFile) {
		t.Fatalf("unknown limit: %v", err)
	}
	h.UnKnownSize = false
	h.UnPackedSize = 99
	rfs.vm.opt.maxReadFileSize = 1
	if _, err := rfs.ReadFile("asd.go"); !errors.Is(err, ErrFileTooLargeForReadFile) {
		t.Fatalf("declared limit: %v", err)
	}

	solid, err := OpenFS(testdataFile(t, "solid-rar5.rar"))
	if err != nil {
		t.Fatal(err)
	}
	for name, node := range solid.ftree {
		h := node.firstBlock()
		if h == nil || !h.Solid || node.isDir() {
			continue
		}
		_, err := solid.Open(name)
		if !errors.Is(err, ErrSolidOpen) {
			t.Fatalf("solid open %s: %v", name, err)
		}
		return
	}
	t.Fatal("solid-rar5.rar has no solid file")
}
