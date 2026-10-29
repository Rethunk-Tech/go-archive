# TODO

## Migrate Mortar onto this module

Deferred. `Rethunk-Tech/mortar` still reads archives through its own
`internal/archive` and does not require this module.

1. Replace those bodies with a facade over this module. Keep the path-based
   `Extract`, `DeclaredSize`, `PreviewArchive` and `RecoverMalformed`.
   `Rethunk-Tech/mortar` `internal/sharesvc/collection_details.go` calls
   `OpenSevenZip`; this module does not export a 7z reader, so that call
   moves onto `Lister`. Keep `StripExtension`, `HasExtension`,
   `PickerPattern`, `SavedName` and `SavedID` (`Rethunk-Tech/mortar` `internal/archive/formats.go`).
   Name the preview bound. Add the
   `WindowsSegment` / `winname.Valid` agreement test. Drop the five decoder
   requirements from Mortar's go.mod.
2. Then update the docs that still describe the copy:
   `Rethunk-Tech/mortar` `docs/security.md`
   `Rethunk-Tech/mortar` `docs/architecture.md`
   `Rethunk-Tech/mortar` `docs/testing.md`

## A 7z over `DefaultMaxEntries` fails as `ErrMalformed`

`sevenZipMaxFolders` is `DefaultMaxEntries` (`sevenzip_limits.go`), so the
header walk refuses before a raised `MaxEntries` can apply. No fixture
declares that many entries. When a caller raises the cap and needs
`ErrTooManyEntries`, narrow the coupling and add the fixture together.

## `TestDiskFullIsDistinguishable` does not fill a disk

It checks that the error chain keeps `ENOSPC`. Forcing a real full filesystem
is more fixture than that fact.

## A solid RAR decodes a skipped entry past its declared size

To reach the next entry, `rardecode` (`decode_reader.go` `nextFile`) drains
the current one under its `limitedReader`, so it decodes all of that entry's
packed data whatever `UnPackedSize` says. `solidCharge` counts the declared
size only. Bounding the real output needs a hook or a patch in rardecode; a
`replace` here would not reach consumers.
