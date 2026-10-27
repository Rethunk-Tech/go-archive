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

It derives build, lint, test with the race detector, vulnerability scan and
workflow lint from the module, and reads the three it cannot infer from
`.gate.toml`. Its binaries come from the `tool` directive in `go.mod` through
`go tool`; never install one globally. CI fetches its own releases, so the pinned
versions in the workflow mirror that directive and have to track it.

`go test` must also pass on Windows, which CI covers with a `windows-latest` job
running the same tests. That material cannot run on Linux: `WindowsSegment`, case
collision detection and `os.Root`'s reserved-name handling are runtime values
there. POSIX file modes are not among them, because Windows synthesises `0666` for
a writable file whatever mode was requested, so the mode assertions run off
Windows.

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
