# Changelog

Notable changes to `go-archive`. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `Extract` reads zip, RAR 4, RAR 5, 7z, tar, and gzip, xz, lzma, zstd or bzip2 alone or around a
  tar, writing through a caller-supplied `*os.Root`.
- `Lister` walks any of those formats forward-only for callers that want names and sizes without
  extraction policy, bounded by `ListerOptions.MaxEntryBytes`.
- `DeclaredSize` sums the sizes an archive's headers declare, bounded so a crafted RAR window cannot
  force the allocation its header names.
- `RecoverMalformed` turns a panic in a format reader into `ErrMalformed`, so a crafted download
  fails its item instead of ending the process.
- `WindowsSegment` reports whether a path segment is a name Windows stores as written, and
  `Options.Segment` replaces it when the destination platform differs.
