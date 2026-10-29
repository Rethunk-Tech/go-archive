# Changelog

Notable changes to `go-archive`. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `Extensions`, `HasExtension`, `StripExtension` and `IsStream` name the file endings of the formats read, for pickers, upload filters and naming the folder an archive unpacks into.
- `IsMediaType` reports whether a `Content-Type` names a format this package reads, including the types `gh release upload` and shared-mime-info give tarballs. `application/octet-stream` is not one.
- `ExtractFunc` walks an archive under `Extract`'s admission rules and hands each entry to a callback instead of writing it, for in-memory or non-filesystem sinks. The bytes the callback reads count against the caps.
- `Options.CaseSensitive` turns off the case-collision check for a sink that keeps names differing only by case apart; an exact duplicate is still refused.
- `ExtractReader` extracts RAR and the stream formats from an `io.Reader` with no random access and no known size, such as an HTTP body. A body that ends before its format shows fails as `io.ErrUnexpectedEOF`.

### Changed

- `baseName` no longer drops a `~~` prefix: a bare stream's file is named from the whole `baseName`, less one stream suffix. A caller with its own prefix convention cuts it first.

### Fixed

- An empty tar named `.tbz` lists and extracts as zero entries, like the other tar endings.

## [0.1.1] - 2026-10-29

### Changed

- The copyright holder is Rethunk.Tech, LLC.

### Fixed

- An empty tar, only its end marker, lists and extracts as zero entries instead of `ErrUnsupportedFormat`, bare or inside a stream, when its name says tar.
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

[Unreleased]: https://github.com/Rethunk-Tech/go-archive/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/Rethunk-Tech/go-archive/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/Rethunk-Tech/go-archive/releases/tag/v0.1.0
