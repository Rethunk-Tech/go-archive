# Changelog

Notable changes to `go-archive`. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- `NOTICE` carries the BSD 2-Clause licence of the vendored rardecode, which binaries built with this module must reproduce.

## [0.1.0] - 2026-10-28

### Added

- `Extract` reads zip, RAR 4, RAR 5, 7z, tar, and gzip, xz, lzma, zstd, bzip2 or lz4 alone or
  around a tar, writing through a caller-supplied `*os.Root`. Zip entries may be stored, Deflate,
  bzip2, LZMA, xz or zstd.
- `Lister` walks any of those formats forward-only for callers that want names and sizes without
  extraction policy, bounded by `ListerOptions.MaxEntryBytes`.
- `DeclaredSize` sums the sizes an archive's headers declare, bounded so a crafted RAR window cannot
  force the allocation its header names.
- `ErrDecoderMemory` reports an archive whose decoder would need more than 256 MiB: a 7z or RAR
  dictionary, or a zstd window. It is not `ErrMalformed`, because such an archive is valid.
- RAR is read through a vendored rardecode that decodes a skipped solid file only to its declared
  size, refusing one that runs past it as `ErrMalformed`.
- `RecoverMalformed` turns a panic in a format reader into `ErrMalformed`, so a crafted download
  fails its item instead of ending the process.
- `WindowsSegment` reports whether a path segment is a name Windows stores as written, and
  `Options.Segment` replaces it when the destination platform differs.

[Unreleased]: https://github.com/Rethunk-Tech/go-archive/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/Rethunk-Tech/go-archive/releases/tag/v0.1.0
