<h1 align="center">go-archive</h1>

<div align="center">

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.27.3+-00ADD8?logo=go&logoColor=white)](go.mod)

</div>

---

One reader for untrusted archives, as a single Go package: `archive`, module
`github.com/Rethunk-Tech/go-archive`. Mortar is its one consumer.

Magic bytes pick the format, never the file extension. Eleven are read: zip, RAR 4, RAR 5, 7z,
tar, and gzip, xz, lzma, zstd, bzip2 or lz4, each alone or wrapped around a tar. A zip entry may
be stored or compressed with Deflate, bzip2, LZMA, xz or zstd. Archives arrive from the
internet with no trust attached, so every reader here is bounded in memory and in bytes before it
produces output.

## Quick start

```bash
go get github.com/Rethunk-Tech/go-archive
```

Rooting, caps, and a worked `Extract` and `Lister` example: [HUMANS.md](HUMANS.md).

## Features

- `Extract` enforces policy: path traversal, unsafe names, case collisions, symlinks and hardlinks,
  device and pipe entries, encryption, and the entry, entry-size, total-size and ratio caps.
- `Lister` walks entries forward-only and applies no extraction policy. Decompression is its only
  bound (`ListerOptions.MaxEntryBytes`).
- The destination must already exist. Writes travel through the caller's `*os.Root`, so no entry
  resolves outside it.
- Decoder windows are capped independently of entry sizes, so a crafted header cannot force a
  multi-gigabyte allocation.
- Fuzzed against every format, with the crash corpus committed.

## Documentation

| Doc | Audience |
| --- | --- |
| [HUMANS.md](HUMANS.md) | Running and using the package |
| [AGENTS.md](AGENTS.md) | Internals, invariants, and why |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Process — commits, tests, docs |
| [SECURITY.md](SECURITY.md) | Vulnerability disclosure |
| [CHANGELOG.md](CHANGELOG.md) | Release notes |

## License

MIT — see [LICENSE](LICENSE). © 2026 Rethunk Technologies. The vendored `internal/rardecode` keeps its BSD 2-Clause licence; see [NOTICE](NOTICE).
