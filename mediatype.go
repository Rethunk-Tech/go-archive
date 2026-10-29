package archive

import "mime"

// IsMediaType reports whether contentType names a format this package reads,
// under the types uploaders and MIME databases give them. Parameters and case
// do not matter. application/octet-stream is not one: it means the sender did
// not know.
func IsMediaType(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	switch mt {
	case "application/zip", "application/x-zip-compressed",
		"application/vnd.rar", "application/x-rar", "application/x-rar-compressed",
		"application/x-7z-compressed",
		"application/x-tar", "application/x-gtar",
		"application/gzip", "application/x-gzip",
		"application/x-xz", "application/x-lzma",
		"application/zstd", "application/x-zstd",
		"application/x-bzip", "application/x-bzip2",
		"application/x-lz4",
		"application/x-compressed-tar", "application/x-gzip-compressed-tar",
		"application/x-xz-compressed-tar", "application/x-lzma-compressed-tar",
		"application/x-zstd-compressed-tar", "application/x-lz4-compressed-tar",
		"application/x-bzip-compressed-tar", "application/x-bzip1-compressed-tar",
		"application/x-bzip2-compressed-tar":
		return true
	}
	return false
}
