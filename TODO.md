# TODO

Decided but not built, and the measurements that settled a question. Behaviour that ships is in
`HUMANS.md` and the code; this file is the only place that describes what does not exist yet.

## Mortar migration is deferred, deliberately

Mortar still carries its own `internal/archive` and does not require this module. That duplication is
the purpose of the split, not a defect to clean up: the package exists so the reader can leave Mortar
and lower its cognitive load. The remaining work is steps 2 and 3 of
`PLAN-go-archive.md` § Order of work, and it starts when someone chooses to start it. Do not "fix" the
duplication by moving Mortar's copy back or by wiring a second reader.

## A 7z declaring more than 20000 entries fails as `ErrMalformed`, not `ErrTooManyEntries`

`sevenZipMaxFolders` is `DefaultMaxEntries`, so the header walk refuses such an archive before a
caller's raised `MaxEntries` can apply, reporting the header invalid rather than the count over the
cap. This is read from `sevenzip_limits.go` and not covered by a fixture: nothing in `testdata/`
declares that many entries, so no test reaches the check, and `MaxEntries: 100000` against
`testdata/valid.7z` extracts cleanly because that archive never approaches it. The outcome is always a
refusal, never a bypass; only the reason differs. Narrow the coupling when a caller raises the cap and
needs the accurate error, and add the fixture at the same time.

## `TestDiskFullIsDistinguishable` wraps an `ENOSPC` rather than driving one

It proves the error chain preserves the errno, which is the contract `Extract` promises. Filling a
real filesystem to force the write failure would cost a fixture far larger than the fact is worth.
