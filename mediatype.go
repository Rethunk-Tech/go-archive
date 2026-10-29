package archive

import "mime"

// mediaTypes are the Content-Type values uploaders and MIME databases give the
// formats this package reads. application/octet-stream is absent: it means the
// sender did not know.
var mediaTypes = map[string]bool{
	"application/zip": true, "application/x-zip-compressed": true,
	"application/vnd.rar": true, "application/x-rar": true, "application/x-rar-compressed": true,
	"application/x-7z-compressed": true,
	"application/x-tar": true, "application/x-gtar": true,
	"application/gzip": true, "application/x-gzip": true,
	"application/x-xz": true, "application/x-lzma": true,
	"application/zstd": true, "application/x-zstd": true,
	"application/x-bzip": true, "application/x-bzip2": true,
	"application/x-lz4": true,
	"application/x-compressed-tar": true, "application/x-gzip-compressed-tar": true,
	"application/x-xz-compressed-tar": true, "application/x-lzma-compressed-tar": true,
	"application/x-zstd-compressed-tar": true, "application/x-bzip-compressed-tar": true,
	"application/x-bzip1-compressed-tar": true, "application/x-bzip2-compressed-tar": true,
	"application/x-lz4-compressed-tar": true,
}

// IsMediaType reports whether contentType names a format this package reads.
// Parameters and case do not matter.
func IsMediaType(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaTypes[mt]
}
