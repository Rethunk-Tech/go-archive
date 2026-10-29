package archive

import (
	"path"
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
	return len(upper) == 4 && upper[3] >= '1' && upper[3] <= '9' &&
		(strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT"))
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
// these five around its payload, never a compound .tar.xz.
var bareSuffixes = []string{".gz", ".xz", ".zst", ".lzma", ".bz2"}

// stripBareSuffix removes one bare-stream suffix, matched case-insensitively.
func stripBareSuffix(name string) string {
	for _, suffix := range bareSuffixes {
		if len(name) >= len(suffix) && strings.EqualFold(name[len(name)-len(suffix):], suffix) {
			return name[:len(name)-len(suffix)]
		}
	}
	return name
}
