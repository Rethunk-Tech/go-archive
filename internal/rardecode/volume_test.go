package rardecode

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func testdataFile(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join("..", "..", "testdata", name)
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func testdataBytes(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(testdataFile(t, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type scriptArc struct {
	volnum int
	old    bool
	initE  error
	next   []error
}

func (s *scriptArc) init(*bufVolumeReader) (int, error) { return s.volnum, s.initE }

func (s *scriptArc) nextBlock(*bufVolumeReader) (*fileBlockHeader, error) {
	if len(s.next) == 0 {
		return nil, io.EOF
	}
	err := s.next[0]
	s.next = s.next[1:]
	if err != nil {
		return nil, err
	}
	return &fileBlockHeader{FileHeader: FileHeader{Name: "x", PackedSize: 0}}, nil
}

func (s *scriptArc) useOldNaming() bool { return s.old }

func TestVolumeNameHelpers(t *testing.T) {
	t.Parallel()

	t.Run("nextNewVolName", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			in, want string
		}{
			{"archive.part1.rar", "archive.part2.rar"},
			{"archive.part001.rar", "archive.part002.rar"},
			{"archive.part001of100.rar", "archive.part002of100.rar"},
			{"part1of2.rar", "part1of3.rar"},
			{"file.1.2.rar", "file.1.3.rar"},
			{"a" + strings.Repeat("9", 40) + ".rar", "a" + strings.Repeat("0", 40) + ".rar"},
		}
		for _, tt := range tests {
			if got := nextNewVolName(tt.in); got != tt.want {
				t.Errorf("nextNewVolName(%q)=%q want %q", tt.in, got, tt.want)
			}
		}
	})

	t.Run("nextOldVolName", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			in, want string
		}{
			{"file.rar", "file.r00"},
			{"file.r00", "file.r01"},
			{"file.r09", "file.r10"},
			{"file.r99", "file.s00"},
			{"file.999", "file.A00"},
			{"file.rarx", "file.r00"},
			{"file.r", "file.r00"},
		}
		for _, tt := range tests {
			if got := nextOldVolName(tt.in); got != tt.want {
				t.Errorf("nextOldVolName(%q)=%q want %q", tt.in, got, tt.want)
			}
		}
	})

	t.Run("fixFileExtension", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			in, want string
		}{
			{"file", "file.rar"},
			{"file.", "file.rar"},
			{"file.exe", "file.rar"},
			{"file.SFX", "file.rar"},
			{"file.rar", "file.rar"},
			{"file.RAR", "file.RAR"},
		}
		for _, tt := range tests {
			if got := fixFileExtension(tt.in); got != tt.want {
				t.Errorf("fixFileExtension(%q)=%q want %q", tt.in, got, tt.want)
			}
		}
	})

	t.Run("hasDigits", func(t *testing.T) {
		t.Parallel()
		if hasDigits("abc") || !hasDigits("a1") {
			t.Fatal("hasDigits")
		}
	})
}

func TestGetOptions(t *testing.T) {
	t.Parallel()
	pw := strings.Repeat("ä", maxPassword+3)
	opt := getOptions([]Option{
		BufferSize(8),
		MaxDictionarySize(1 << 20),
		MaxReadFileSize(9),
		FileSystem(fstest.MapFS{}),
		Password(pw),
		SkipCheck,
		OpenFSCheck,
	})
	if opt.bsize != 8 || opt.maxDictSize != 1<<20 || opt.maxReadFileSize != 9 {
		t.Fatalf("sizes: %+v", opt)
	}
	if opt.fs == nil || !opt.skipCheck || !opt.openCheck {
		t.Fatal("flags")
	}
	if opt.pass == nil || len([]rune(*opt.pass)) != maxPassword {
		t.Fatalf("password runes=%v", opt.pass)
	}
	plain := getOptions([]Option{Password("ok")})
	if plain.pass == nil || *plain.pass != "ok" {
		t.Fatal("short password")
	}
}

func TestOSFSOpen(t *testing.T) {
	t.Parallel()
	f, err := defaultFS.Open(testdataFile(t, "real.rar"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	_, err = defaultFS.Open(filepath.Join("..", "..", "testdata", "missing.rar"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
}

func TestNewVolumeAndReader(t *testing.T) {
	t.Parallel()
	rar := testdataBytes(t, "real.rar")

	t.Run("unknown_version", func(t *testing.T) {
		t.Parallel()
		_, err := newVolume(bytes.NewReader([]byte("Rar!\x1a\x07\x02\x00xxxx")), getOptions(nil), 0)
		if !errors.Is(err, ErrUnknownVersion) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("truncated", func(t *testing.T) {
		t.Parallel()
		_, err := newVolume(bytes.NewReader([]byte("Rar!")), getOptions(nil), 0)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("version_mismatch", func(t *testing.T) {
		t.Parallel()
		v := &readerVolume{opt: getOptions(nil), arc: &scriptArc{}, ver: archiveVersion15}
		err := v.init(bytes.NewReader(rar), 0)
		if !errors.Is(err, ErrVerMismatch) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("bad_volume_number", func(t *testing.T) {
		t.Parallel()
		v := &readerVolume{opt: getOptions(nil)}
		if err := v.init(bytes.NewReader(rar), 0); err != nil {
			t.Fatal(err)
		}
		v.arc = &scriptArc{volnum: 7}
		err := v.init(bytes.NewReader(rar), 0)
		if !errors.Is(err, ErrBadVolumeNumber) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("read_and_block", func(t *testing.T) {
		t.Parallel()
		v, err := newVolume(bytes.NewReader(rar), getOptions(nil), 0)
		if err != nil {
			t.Fatal(err)
		}
		if !v.canSeek() {
			t.Fatal("bytes.Reader should seek")
		}
		h, err := v.nextBlock()
		if err != nil {
			t.Fatal(err)
		}
		if h.Name != "asd.go" {
			t.Fatalf("name %q", h.Name)
		}
		n, err := v.Read(make([]byte, 1))
		if n != 1 || err != nil {
			t.Fatalf("Read n=%d err=%v", n, err)
		}
		b, err := v.ReadByte()
		if err != nil && b == 0 && v.n == 0 {
			t.Fatalf("ReadByte err=%v", err)
		}
		_, _ = v.writeToAtMost(io.Discard, 0)
		_, _ = v.writeToAtMost(io.Discard, 2)
		_, _ = v.writeToAtMost(io.Discard, -1)
		v.n = 0
		if n, err := v.Read(make([]byte, 4)); n != 0 || err != io.EOF {
			t.Fatalf("empty Read n=%d err=%v", n, err)
		}
		if _, err := v.ReadByte(); err != io.EOF {
			t.Fatalf("empty ReadByte %v", err)
		}
		v.n = 1 << 20
		var rerr error
		for rerr == nil {
			_, rerr = v.Read(make([]byte, 64))
		}
		if !errors.Is(rerr, io.ErrUnexpectedEOF) && rerr != io.EOF {
			t.Fatalf("short Read %v", rerr)
		}
		v.n = 3
		_, rerr = v.ReadByte()
		if !errors.Is(rerr, io.ErrUnexpectedEOF) && rerr != io.EOF {
			t.Fatalf("short ReadByte %v", rerr)
		}
		if err := v.openBlock(1, 0, 1); !errors.Is(err, ErrBadVolumeNumber) {
			t.Fatalf("openBlock vol %v", err)
		}
		if err := v.openBlock(0, h.dataOff, 1); err != nil {
			t.Fatalf("openBlock: %v", err)
		}
		v.n = 4
		if _, err := v.nextBlock(); err == nil {
			t.Fatal("expected discard or next error")
		}
	})
}

func TestVolumeManager(t *testing.T) {
	t.Parallel()
	rar := testdataBytes(t, "real.rar")
	mfs := fstest.MapFS{
		"archive.part1.rar": {Data: rar},
		"archive.part2.rar": {Data: rar},
		"plain.rar":         {Data: rar},
		"plain.r00":         {Data: rar},
		"plain.r01":         {Data: rar},
		"bad.rar":           {Data: []byte("not a rar")},
	}
	opt := getOptions([]Option{FileSystem(mfs)})

	t.Run("files_and_new_names", func(t *testing.T) {
		t.Parallel()
		vm := &volumeManager{files: []string{"archive.part1.rar"}, opt: opt}
		f, err := vm.openVolumeFile(1)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
		if got := vm.Files(); len(got) < 2 || got[1] != "archive.part2.rar" {
			t.Fatalf("files %v", got)
		}
	})

	t.Run("old_names", func(t *testing.T) {
		t.Parallel()
		vm := &volumeManager{files: []string{"plain.rar"}, opt: opt}
		f, err := vm.openVolumeFile(1)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
		if !vm.old || vm.files[1] != "plain.r00" {
			t.Fatalf("old=%v files=%v", vm.old, vm.files)
		}
		f, err = vm.openVolumeFile(0)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	})

	t.Run("try_new_missing", func(t *testing.T) {
		t.Parallel()
		vm := &volumeManager{files: []string{"missing.part1.rar"}, opt: opt}
		_, err := vm.tryNewName("missing.part1.rar")
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("try_old_fallback", func(t *testing.T) {
		t.Parallel()
		fsys := fstest.MapFS{
			"x1.rar": {Data: rar},
			"x1.r00": {Data: rar},
		}
		vm := &volumeManager{files: []string{"x1.rar"}, opt: getOptions([]Option{FileSystem(fsys)})}
		f, err := vm.tryNewName("x1.rar")
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
		if !vm.old || vm.files[len(vm.files)-1] != "x1.r00" {
			t.Fatalf("old fallback files=%v old=%v", vm.files, vm.old)
		}
	})

	t.Run("newVolume_bad", func(t *testing.T) {
		t.Parallel()
		vm := &volumeManager{files: []string{"bad.rar"}, opt: opt}
		_, err := vm.newVolume(0)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("openVolume", func(t *testing.T) {
		t.Parallel()
		v, err := openVolume("plain.rar", opt)
		if err != nil {
			t.Fatal(err)
		}
		defer v.Close()
		h := &fileBlockHeader{FileHeader: FileHeader{PackedSize: 4}}
		_, err = v.vm.openBlockOffset(h, 0)
		if !errors.Is(err, ErrInvalidHeaderOff) {
			t.Fatalf("offset: %v", err)
		}
		h.Solid = true
		_, err = v.vm.openArchiveFile(&fileBlockList{blocks: []*fileBlockHeader{h}})
		if !errors.Is(err, ErrSolidOpen) {
			t.Fatalf("solid: %v", err)
		}
		if err := v.openBlock(1, 0, 1); err != nil {
			t.Fatalf("fileVolume.openBlock: %v", err)
		}
		v.arc = &scriptArc{volnum: -1, next: []error{ErrMultiVolume, nil}}
		if _, err := v.nextBlock(); err != nil {
			t.Fatalf("multi: %v", err)
		}
		v.arc = &scriptArc{volnum: -1, next: []error{errVolumeOrArchiveEnd}}
		v.vm = &volumeManager{files: []string{"only.rar"}, opt: opt, old: true}
		if _, err := v.nextBlock(); !errors.Is(err, io.EOF) {
			t.Fatalf("end: %v", err)
		}
	})
}

func FuzzNewVolume(f *testing.F) {
	if b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "real.rar")); err == nil {
		f.Add(b)
	}
	f.Add([]byte("Rar!\x1a\x07\x01\x00"))
	f.Add([]byte("Rar!\x1a\x07\x00"))
	f.Add([]byte("Rar!"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		v, err := newVolume(bytes.NewReader(data), getOptions(nil), 0)
		if err != nil {
			return
		}
		h, err := v.nextBlock()
		if err != nil {
			return
		}
		_, _ = v.Read(make([]byte, 8))
		_, _ = v.ReadByte()
		_, _ = v.writeToAtMost(io.Discard, h.PackedSize)
	})
}
