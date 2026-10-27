module github.com/Rethunk-Tech/go-archive

go 1.27.3

// The gate's tool versions belong to the module, not to whatever CI happens to
// have installed globally: a linter or scanner bump arrives as a diff to this
// file rather than as a silently changed verdict.
tool (
	github.com/golangci/golangci-lint/v2/cmd/golangci-lint
	golang.org/x/vuln/cmd/govulncheck
)

require (
	github.com/bodgit/sevenzip v1.6.5
	github.com/klauspost/compress v1.20.3
	github.com/nwaples/rardecode/v2 v2.4.5
	github.com/ulikunitz/xz v0.5.17
	golang.org/x/text v0.43.0
)

require (
	github.com/bodgit/plumbing v1.3.0 // indirect
	github.com/bodgit/windows v1.0.1 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/pierrec/lz4/v4 v4.1.33 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/stangelandcl/ppmd v0.1.1 // indirect
	go4.org v0.0.0-20261019143151-5b882263cdaf // indirect
)
