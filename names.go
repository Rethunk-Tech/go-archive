package archive

import (
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// WindowsSegment reports whether one path segment is a name Windows stores as
// written. Windows drops a trailing dot or space, reads the characters in
// windowsUnsafe as separators or invalid, and hands a device name to any file
// whose stem matches one.
func WindowsSegment(seg string) bool {
	if seg == "" || strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") {
		return false
	}
	for _, r := range seg {
		if r < ' ' || strings.ContainsRune(windowsUnsafe, r) {
			return false
		}
	}
	stem, _, _ := strings.Cut(seg, ".")
	return !isDeviceName(stem)
}

// windowsUnsafe are the printable characters Windows refuses in a file name;
// it refuses the control characters besides.
const windowsUnsafe = `<>:"/\|?*`

// isDeviceName reports whether stem names a device Windows reserves in every
// directory whatever extension follows, so CON.txt is as unusable as CON.
func isDeviceName(stem string) bool {
	upper := strings.ToUpper(strings.TrimRight(stem, " "))
	switch upper {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	port, ok := strings.CutPrefix(upper, "COM")
	if !ok {
		port, ok = strings.CutPrefix(upper, "LPT")
	}
	// The ports run 0 to 9, and Windows also reserves the superscript ¹, ² and ³.
	return ok && utf8.RuneCountInString(port) == 1 && strings.ContainsAny(port, "0123456789¹²³")
}

// utf8Name is a zip entry's name as UTF-8. Zips written without the UTF-8 flag
// carry names in the writer's locale, most often GBK from Chinese-locale tools,
// which GB18030 decodes. Left as raw bytes the name extracts to a directory the
// caller's own runtime cannot open.
func utf8Name(name string) string {
	if utf8.ValidString(name) {
		return name
	}
	decoded, err := simplifiedchinese.GB18030.NewDecoder().String(name)
	if err != nil || !utf8.ValidString(decoded) {
		return strings.ToValidUTF8(name, "_")
	}
	return decoded
}

func cleanName(name string, opts Options) (string, error) {
	name = strings.ReplaceAll(name, `\`, "/")
	if strings.ContainsRune(name, 0) {
		return "", ErrUnsafeName
	}
	if strings.HasPrefix(name, "/") {
		return "", ErrTraversal
	}
	segment := opts.segment()
	var segs []string
	for s := range strings.SplitSeq(name, "/") {
		switch s {
		case "", ".":
			continue
		case "..":
			return "", ErrTraversal
		}
		if !segment(s) {
			return "", ErrUnsafeName
		}
		segs = append(segs, s)
	}
	return path.Join(segs...), nil
}

// bareSuffixes are the only suffixes a bare compressed stream carries: one of
// these around its payload, never a compound .tar.xz.
var bareSuffixes = []string{".gz", ".xz", ".zst", ".lzma", ".bz2", ".lz4"}

// extensions are the file name endings of the formats read, longest first so a
// compound ending wins over its last part.
var extensions = []string{
	".tar.gz", ".tar.xz", ".tar.zst", ".tar.bz2", ".tar.lzma", ".tar.lz4",
	".tgz", ".txz", ".tzst", ".tbz2", ".tbz",
	".zip", ".rar", ".7z", ".tar", ".gz", ".xz", ".zst", ".lzma", ".bz2", ".lz4",
}

// Extensions are the file name endings of the formats this package reads,
// longest first. Detection never trusts them; they are for naming and picking
// files. Each call returns a fresh slice.
func Extensions() []string { return slices.Clone(extensions) }

// HasExtension reports whether name ends in one of Extensions, ignoring case.
func HasExtension(name string) bool {
	_, ok := extensionOf(name)
	return ok
}

// StripExtension is name without its archive ending, or name itself when it
// has none.
func StripExtension(name string) string {
	ext, _ := extensionOf(name)
	return name[:len(name)-len(ext)]
}

// IsStream reports whether name's archive ending is a bare compressed stream
// (.gz, .xz, …) rather than a tar or a container. Such a file holds one
// unnamed payload, which Extract names from baseName.
func IsStream(name string) bool {
	ext, _ := extensionOf(name)
	return slices.Contains(bareSuffixes, ext)
}

// isTarName reports whether name ends in a tar ending, bare or compressed.
// Every such ending, and no other in the table, starts ".t".
func isTarName(name string) bool {
	ext, _ := extensionOf(name)
	return strings.HasPrefix(ext, ".t")
}

// extensionOf is name's longest archive ending, lower-cased, or "" when it has
// none.
func extensionOf(name string) (string, bool) {
	lower := strings.ToLower(name)
	for _, ext := range extensions {
		if strings.HasSuffix(lower, ext) {
			return ext, true
		}
	}
	return "", false
}

// stripBareSuffix removes one bare-stream suffix, matched case-insensitively.
func stripBareSuffix(name string) string {
	for _, suffix := range bareSuffixes {
		if len(name) >= len(suffix) && strings.EqualFold(name[len(name)-len(suffix):], suffix) {
			return name[:len(name)-len(suffix)]
		}
	}
	return name
}
