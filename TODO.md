# TODO

## A 7z over `DefaultMaxEntries` fails as `ErrMalformed`

`sevenZipMaxFolders` is `DefaultMaxEntries` (`sevenzip_limits.go`), so the
header walk refuses before a raised `MaxEntries` can apply. No fixture
declares that many entries. When a caller raises the cap and needs
`ErrTooManyEntries`, narrow the coupling and add the fixture together.
