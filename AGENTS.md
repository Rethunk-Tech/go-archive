# AGENTS.md

Package `archive` reads untrusted archives. It has one consumer and exports only
what that consumer calls; anything else stays unexported, including both 7z entry
points and every cap no user-facing message quotes.

The module is standalone. It imports no consumer package and is gated with the
consumer nowhere in the loop.

## Gate

`gate` is the one command:

```sh
gate
```

`gate` derives build and test from the module, and finds lint, vuln and workflow
lint only if they are on PATH. A CI runner has none of them, so `.gate.toml`
names all five: lint, vuln and format through the `tool` directive in `go.mod`
via `go tool`, workflow lint through `scripts/actionlint`, and the two Windows
gates no Linux tool reaches. A gate that a runner cannot resolve is skipped
without failing the run, which is why the override has to exist rather than
being left to detection.

That also means the binaries come from the module and never from a global
install — a linter bump is a diff to `go.mod`, not a silently changed verdict.
`scripts/actionlint` is the exception: actionlint 1.7.12 no longer compiles
against the `yaml.v3` the module graph resolves, so the script pins its release
and checksum instead of building it through `go tool`. CI fetches its own
releases, so the pinned versions in the workflow mirror both and have to track
them.

`go test` must also pass on Windows, which CI covers with a `windows-latest` job
running the same tests. `WindowsSegment` and the case-collision bookkeeping are
the package's own logic and are verified on Linux, but `os.Root`'s reserved-name
refusal is enforced by the platform, so only that job reaches it. POSIX file
modes are also platform-dependent: Windows synthesises `0666` for a writable file
whatever mode was requested, so the mode assertions run off Windows.

## Shape

One unexported `walk` enumerates an archive's entries. `Extract` and `Lister` both
go through it, which is what keeps them from disagreeing about what an archive
holds. Their bounds differ and that difference is the design:

| | `Extract` | `Lister` |
| --- | --- | --- |
| Entry cap | enforces `MaxEntries` | none; counting is the caller's policy |
| Names and kinds | refuses traversal, unsafe names, links, special files, encryption | reports what the archive holds |
| Bytes | caps every read against the extraction caps | caps each entry at `ListerOptions.MaxEntryBytes` |
| Destination | writes through the caller's `*os.Root` | writes nothing |

A change that makes a listing cap entries is the invariant breaking, not a
feature. `Extract` refuses an over-declared zip or 7z entry count before writing,
so the refusal leaves no partial tree.

Two decompression bounds are not policy and cannot be lowered away: the decoder
window (`maxDecoderBytes`) is set where the decoder is built, and a stream's
aggregate budget stops a bomb the per-entry bound would let through spread thin.

## Comments

Say why, not history. No "previously", no ticket or finding IDs, no benchmark
figures, and no record of an alternative that was rejected: those drift, and git
already holds them.
