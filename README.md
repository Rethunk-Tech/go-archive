# go-archive

Package `archive`, module `github.com/Rethunk-Tech/go-archive`: one reader for untrusted archives,
with exactly one consumer. Magic bytes pick the format — zip, RAR 4 and RAR 5, 7z, tar, and gzip,
xz, lzma, zstd or bzip2, alone or wrapped around a tar. The extension is never consulted.

`Extract` validates and enumerates with policy. It refuses traversal, unsafe names, case collisions,
links, special files, encryption and every cap breach, stops at the first error, and leaves the
partial tree behind. It takes a `*os.Root` the caller opened rather than a path, and the destination
directory must already exist, because `os.OpenRoot` fails on a missing one.

`Lister` only bounds decompression and knows nothing about extraction policy: entry counts,
truncation and name acceptability are the caller's. It is forward-only, as the formats are — `Open`
is valid only until the next `Next`, `Next` ends with `io.EOF`, and `Close` is idempotent and never
closes the caller's `Source`. Reading an entry is optional; `Entry.Name` normalises `\` to `/` and
drops a trailing `/`.

`ListerOptions.MaxEntryBytes` of zero means 256 MiB, deliberately tight: a caller that needs more
must say so. Zero `Options` fields take the `Default*` caps. Go 1.27.3+; `src` is a `Source`, `size` its length.

## Extract

```go
func extract(src archive.Source, size int64, baseName, dest string) error {
	root, err := os.OpenRoot(dest) // dest must exist
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return archive.Extract(src, size, baseName, root, archive.Options{})
}
```

## List

```go
func list(src archive.Source, size int64, baseName string) error {
	l, err := archive.NewLister(src, size, baseName, archive.ListerOptions{MaxEntryBytes: 8 << 30})
	if err != nil {
		return err
	}
	defer func() { _ = l.Close() }()
	for {
		e, err := l.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if !e.IsDir {
			fmt.Println(e.Name, e.Size)
		}
	}
}
```

## Licence

MIT. See `LICENSE`.
