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
l, err := archive.NewLister(f, info.Size(), filepath.Base(path), archive.ListerOptions{})
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

| | Contract |
| --- | --- |
| `Extract` refuses | path traversal, disallowed names, case collisions, symlinks and hardlinks, device/pipe/socket entries, encrypted archives, and archives over the entry, entry-size, total-size or ratio caps |
| `ExtractFunc` | admits as `Extract` does and passes the cleaned slash path to `fn`; counts the bytes `fn` reads against the caps, checks a file's checksum only when `fn` reads it to the end, and returns `fn`'s own error unwrapped. Nothing judges the tree, so a file `a` beside `a/b` passes |
| `ExtractReader` | RAR and the stream formats only; zip and 7z are `ErrUnsupportedFormat`. The size is unknown, so `MaxTotalBytes` is the bound with no ratio. A read error is returned in the chain, and a body that ends before its format shows is `io.ErrUnexpectedEOF` |
| `Options.CaseSensitive` | off by default; on, names differing only by case are both admitted, for an `ExtractFunc` sink that keeps them apart. An exact duplicate file is still refused |
| `Extract` on failure | stops at the first error and leaves the partial tree, so pass a throwaway directory and discard it |
| `baseName` | names the file a bare stream yields: the whole name less one trailing stream suffix (`.gz`, `.xz`, `.zst`, `.lzma`, `.bz2`, `.lz4`), matched case-insensitively; formats carrying their own names ignore it |
| An empty tar | 1,024 zero bytes read as a tar with no entries only when `baseName` has a tar ending (`.tar`, `.tar.*`, `.tgz`, `.txz`, `.tzst`, `.tbz2`, `.tbz`); under any other name they are refused, since zeros are otherwise a valid stream payload |
| `Lister` refuses | only what no reader can open safely: a 7z whose headers name more than `DefaultMaxEntries` entries, a decoder over 256 MiB, or an encrypted header; `Open` refuses an encrypted entry; entry counts and name acceptability are otherwise the caller's |
| `Lister`'s bounds | `ListerOptions.MaxEntryBytes` on what `Open` hands out, and `Extract`'s default stream budget with its ratio, because a stream, and a solid 7z or RAR reaching a later entry, decompresses what the caller skips |
| The walk | forward-only, because the formats are; `Open` is valid only until the next `Next` |
| `Close` | idempotent, and never closes the `Source` you passed in; every read is at an offset, so its position never moves |
| `Entry.Name` | normalised: `\` becomes `/`, a trailing `/` is dropped |
| A skipped entry | reports the size its header declared, which an archive can set to anything; a RAR entry of unknown size reports 0 and a bare stream `-1` |
| `Entries()` | reports each header's size and drains only a bare stream, whose `-1` becomes the bytes actually there |

## Caps

A zero `Options` field takes its `Default*` value: 32 GiB per entry, 64 GiB total, 20000 entries,
a 1024 ratio above a 2 GiB floor.

`ListerOptions.MaxEntryBytes` of zero means 256 MiB. That default is deliberately tighter than an
extraction cap, because it bounds a decompressor on input that has not been validated. A caller
listing large streams must set the bound it actually needs.

## Requirements

Go 1.27.3 or newer, plus `lefthook` and `gitleaks` on PATH if you want the git hooks.

```bash
lefthook install
```

That is per clone; git does not commit hooks. Pre-commit scans staged changes for secrets, and
pre-push runs `gate`.

## Verify

```bash
gate
```
