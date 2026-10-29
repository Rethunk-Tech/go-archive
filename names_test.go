package archive

import (
	"errors"
	"testing"
)

func TestWindowsSegment(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		// Ordinary names.
		"a.txt": true, "Mod": true, "run.sh": true, "com10.txt": true, "lpt10": true,
		"CONSOLE": true, "com1x": true, "null": true, "auxiliary": true,
		"‘quoted’": true, "ファイル.txt": true, "a b": true, "a-b_c": true,

		// Device names, matched on the stem so an extension does not rescue them.
		"lpt0": false, "COM0.txt": false, "com¹": false, "LPT³.log": false,
		"CON": false, "con": false, "CON.txt": false, "nul": false, "NUL.tar.gz": false,
		"aux": false, "prn": false, "com1": false, "COM9.txt": false, "lpt1": false,
		"LPT9.dll": false, "CONIN$": false, "CONOUT$": false, "CON ": false,

		// Characters Windows reads as separators or refuses outright.
		"a:b": false, "a?b": false, "a*b": false, `a\b`: false, "a/b": false,
		"a<b": false, "a>b": false, "a|b": false, `a"b`: false,

		// Windows drops a trailing dot or space, so the stored name would differ.
		"foo.": false, "foo ": false, ".": false, "..": false,

		// Control characters, and the empty segment.
		"": false, "a\tb": false, "a\nb": false, "a\x00b": false, "a\x1fb": false,
	}
	for seg, want := range cases {
		if got := WindowsSegment(seg); got != want {
			t.Errorf("WindowsSegment(%q) = %t, want %t", seg, got, want)
		}
	}
}

// Segment is the one hook, so a caller with a stricter policy replaces the
// Windows rules entirely rather than layering on top of them.
func TestCleanNameUsesTheCallerSegmentPolicy(t *testing.T) {
	t.Parallel()
	opts := Options{Segment: func(seg string) bool { return seg != "blocked" }}
	if _, err := cleanName("blocked/x", opts); err == nil {
		t.Fatal("the caller's policy was not consulted")
	}
	if got, err := cleanName("CON/x", opts); err != nil || got != "CON/x" {
		t.Fatalf("cleanName = %q, %v; want the default rules replaced", got, err)
	}
	if _, err := cleanName("blocked/x", Options{}); err != nil {
		t.Fatalf("the default policy refused an ordinary name: %v", err)
	}
}

func TestCleanNameRejectsTraversalAndUnsafeNames(t *testing.T) {
	t.Parallel()
	bad := map[string]error{
		"../x":      ErrTraversal,
		"/x":        ErrTraversal,
		`..\x`:      ErrTraversal,
		"a/../../b": ErrTraversal,
		"a\x00b":    ErrUnsafeName,
		"CON":       ErrUnsafeName,
		"a/foo.":    ErrUnsafeName,
	}
	for name, want := range bad {
		if _, err := cleanName(name, Options{}); !errors.Is(err, want) {
			t.Errorf("cleanName(%q) = %v, want %v", name, err, want)
		}
	}
}

func TestCleanNameJoinsSegments(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"a/b.txt": "a/b.txt",
		`a\b\c`:   "a/b/c",
		"a//b":    "a/b",
		"a/./b":   "a/b",
		"./a":     "a",
		"a/":      "a",
		"":        "",
		".":       "",
	}
	for in, want := range cases {
		got, err := cleanName(in, Options{})
		if err != nil || got != want {
			t.Errorf("cleanName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestStripBareSuffixMatchesCaseInsensitively(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"notes.txt.gz": "notes.txt", "notes.txt.XZ": "notes.txt", "notes.txt.zst": "notes.txt",
		"notes.txt.lzma": "notes.txt", "notes.txt.bz2": "notes.txt",
		// One suffix comes off, never two: the compound is the caller's business,
		// and singleName only runs for a stream that is not a tar.
		"m.tar.gz": "m.tar", "plain": "plain", "gz": "gz",
	}
	for in, want := range cases {
		if got := stripBareSuffix(in); got != want {
			t.Errorf("stripBareSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}

// A leading "id~~" is a caller's prefix, not part of the stream's name.
func TestSingleNameStripsTheSavedIDSeparator(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"0a1b2c~~notes.txt.gz": "notes.txt",
		"id~~m.tar":            "m.tar",
		"notes.txt.gz":         "notes.txt",
		"":                     "file",
		".":                    "file",
	}
	for in, want := range cases {
		if got := singleName(in); got != want {
			t.Errorf("singleName(%q) = %q, want %q", in, got, want)
		}
	}
}
