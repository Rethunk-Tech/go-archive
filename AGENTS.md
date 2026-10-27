# AGENTS.md

Package `archive` reads untrusted archives. It exports only what its consumer
calls. Both 7z entry points stay unexported, and so does every cap no
user-facing message quotes. The module imports no consumer package.

## Gate

```sh
gate
```

`.gate.toml` names the gates a runner cannot resolve from PATH. Lint, vuln and
format run through `go tool`, so their versions are the `tool` directive in
`go.mod`. Workflow lint is `scripts/actionlint`: actionlint 1.7.12 does not
compile against the `yaml.v3` this module resolves, so the script pins a
release and checksum. CI fetches its own copies of the same pins.

`WindowsSegment` and case-collision bookkeeping run on Linux. `os.Root`'s
reserved-name refusal is the platform's, so only the `windows-latest` job
reaches it. Windows synthesises mode `0666` for a writable file, so mode
assertions run off Windows.

## Shape

One package. An unexported `walk` is the only enumerator; `Extract` and
`Lister` both use it. `item`, `walkFunc` and `walkLimits` stay unexported.
A second package would have to export them to keep one enumerator, which is
a larger surface. Tests are in-package for the same reason.

| | `Extract` | `Lister` |
| --- | --- | --- |
| Entry cap | enforces `MaxEntries` | none; counting is the caller's policy |
| Names and kinds | refuses traversal, unsafe names, links, special files, encryption | reports what the archive holds |
| Bytes | caps every read against the extraction caps | caps each entry at `ListerOptions.MaxEntryBytes` |
| Destination | writes through the caller's `*os.Root` | writes nothing |

A listing that caps entries breaks that split. `Extract` refuses an
over-declared zip or 7z entry count before writing, so the refusal leaves no
partial tree.

`maxDecoderBytes` is set where the decoder is built. A stream's aggregate
budget stops a bomb the per-entry bound would allow spread thin. Neither is
policy, and neither can be lowered away.

## Files

| File | Holds |
| --- | --- |
| `archive.go` | package doc, cap constants, `Options`, `Source`, `Extract` |
| `walk.go` | `walk`, per-format enumerators, entry-mode mapping |
| `extract.go` | name admission, case collisions, writing, byte caps |
| `stream.go` | streaming-format detection, decoder window, stream budget |
| `detect.go` | format identifiers and magic-byte detection |
| `sevenzip_limits.go` | 7z header walk, before the reader sees the archive |
| `lister.go`, `names.go`, `size.go`, `recover.go` | one export each |

## Comments

Say why. Git holds the history.
