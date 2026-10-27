# HUMANS.md

Run and use `go-archive`. Internals: [AGENTS.md](AGENTS.md).

## Quick start

```bash
go get github.com/Rethunk-Tech/go-archive
```

`Extract` writes to disk. The caller opens the destination root, because `os.Root` is what confines
every write:

```go
f, err := os.Open(path)
if err != nil {
 return err
}
defer func() { _ = f.Close() }()
info, err := f.Stat()
if err != nil {
 return err
}

root, err := os.OpenRoot(dest) // dest must exist before this line
if err != nil {
 return err
}
defer func() { _ = root.Close() }()

err = archive.Extract(f, info.Size(), filepath.Base(path), root, archive.Options{})
```

`Lister` enumerates without writing anything:

```go
l, err := archive.NewLister(f, info.Size(), filepath.Base(path), archive.ListerOptions{
 MaxEntryBytes: 8 << 30,
})
if err != nil {
 return err
}
defer func() { _ = l.Close() }()

for {
 e, err := l.Next()
 if errors.Is(err, io.EOF) {
  break
 }
 if err != nil {
  return err
 }
 if e.IsDir {
  continue
 }
 fmt.Println(e.Name, e.Size)
}
```

## Contracts

`Extract` refuses path traversal, names that are not allowed, case collisions, symlinks and
hardlinks, device/pipe/socket entries, encrypted archives, and archives over the entry, entry-size,
total-size or ratio caps. It stops at the first error and leaves the partial tree, so pass a
throwaway directory and discard it on failure.

`Lister` applies no entry caps, no destination and no naming policy. Entry counts and name
acceptability are the caller's. The one bound it keeps is `ListerOptions.MaxEntryBytes`, because the
stream formats cannot be listed without decompressing them.

The walk is forward-only, because the formats are. `Open` is valid only until the next `Next`, which
returns `io.EOF` at the end. `Close` is idempotent and never closes the `Source` you passed in.
`Entry.Name` is normalised, with `\` becoming `/` and a trailing `/` dropped.

Reading an entry is optional, and skipping one is cheaper than reading it. A skipped entry reports the
size its header declared, which an archive can set to anything; a bare compressed stream declares none
and reports `-1`. Use `Entries()` when you need the bytes actually there, since it drains each entry
and reports the real count.

## Caps

A zero `Options` field takes its `Default*` value: 32 GiB per entry, 64 GiB total, 20000 entries,
a 1024 ratio above a 2 GiB floor.

`ListerOptions.MaxEntryBytes` of zero means 256 MiB. That default is deliberately tighter than an
extraction cap, because it bounds a decompressor on input that has not been validated. A caller
listing large streams must set the bound it actually needs.

## Requirements

Go 1.27.3 or newer.

## Verify

```bash
gate
```

Licensed under MIT; see [LICENSE](LICENSE).
