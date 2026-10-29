package archive

import "testing"

func TestIsMediaType(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"application/zip":                    true,
		"Application/Zip; charset=binary":    true,
		"application/x-gtar":                 true,
		"application/x-compressed-tar":       true,
		"application/x-bzip1-compressed-tar": true,
		"application/x-lz4":                  true,
		"application/x-rar":                  true,
		"application/octet-stream":           false,
		"text/plain":                         false,
		"":                                   false,
		"not a type;;":                       false,
	}
	for ct, want := range cases {
		if got := IsMediaType(ct); got != want {
			t.Errorf("IsMediaType(%q) = %v, want %v", ct, got, want)
		}
	}
}
