# AGENTS.md

Package `archive` reads untrusted archives. It exports only what its consumer
calls. Both 7z entry points stay unexported, and so does every cap no
user-facing message quotes. The module imports no consumer package.

## Gate

`gate` is the command. Each override in `.gate.toml` is there because a runner
cannot resolve that check from PATH.

`WindowsSegment` and case-collision comparisons run on Linux. Two things do
not. `os.Root` refusing a reserved Windows name is the platform's, so only
the `windows-latest` job reaches it. Windows reports mode `0666` for a
writable file, so the `0o600` and `0o750` assertions run off Windows.

## Shape

One package. `walk` is the only enumerator, and it is unexported. `Extract`,
`ExtractFunc`, `ExtractReader` and `Lister` all use it, and the three extractors
share one admission path (`extractor.admitEntry`) with only the sink differing,
so they cannot disagree about what an archive may hold. How their bounds differ is the contract in
[HUMANS.md](HUMANS.md). `item`, `walkFunc` and `walkLimits` stay unexported;
exporting them is what a second package would cost.

`internal/rardecode` is nwaples/rardecode v2.4.5 with one patch, marked
`go-archive:` in `decode_reader.go` and `reader.go`: a skipped file in a solid
archive decodes only to its declared size. Upstream changes are merged by hand;
it is excluded from lint and format (`canon.toml`).

`Extract` refuses an over-declared zip or 7z entry count before writing.
`maxDecoderBytes` is set where the decoder is built. A stream's aggregate
budget, capped by the ratio, stops a bomb the per-entry bound would allow
spread thin, including one in an entry nothing writes. Neither
bound is policy.

The caps are this package's own `limitReader`, not go-safefs's `CapReader`: each
bound fails with its own typed reason (`ErrEntryTooLarge`, the total, the ratio),
which a single `ErrTooLarge` cannot carry, so the module takes no go-safefs import.

## Files

| File | Holds |
| --- | --- |
| `archive.go` | package doc, cap constants, `Options`, `Source`, `Extract`, `ExtractFunc`, `ExtractReader` |
| `errors.go` | the `Err*` sentinels, `Error` (an entry name with its reason) and `wrap` |
| `walk.go` | `walk`, per-format enumerators, entry-mode mapping |
| `extract.go` | name admission, case collisions, byte caps, the root writer |
| `stream.go` | streaming-format detection, decoder window, stream budget |
| `detect.go` | format identifiers and magic-byte detection |
| `sevenzip_limits.go` | 7z header walk, before the reader sees the archive |
| `lister.go` | `Lister` |
| `names.go` | `WindowsSegment`, name cleaning, the extension table |
| `mediatype.go` | `IsMediaType` |
| `size.go` | `DeclaredSize` |
| `recover.go` | `RecoverMalformed` |
