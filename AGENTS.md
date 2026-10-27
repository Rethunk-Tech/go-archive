# AGENTS.md

Internals for changing this repo. Usage: [HUMANS.md](HUMANS.md). Process: [CONTRIBUTING.md](CONTRIBUTING.md).

## The one invariant

**`Extract` enforces policy and `Lister` does not.** Both drive one unexported walk, so they
enumerate the same entries in the same order; they differ in what is layered on top. A change that
puts extraction policy (caps, destination, name rules) into the walk or the `Lister` breaks the
contract for every listing caller.

## Shape

The module has exactly one consumer and exports only what that consumer calls. Anything it does not
call stays unexported, including both 7z entry points. A separate repository cannot use Go's
`internal/`, so this list is the discipline: if the consumer does not call it, it is not exported.

The module must never import the donor project it was copied out of, or any of that project's
packages. It is standalone, and its gate runs with that project nowhere in the loop.

## File map

| Path | Holds |
| --- | --- |
| [README.md](README.md) | Orientation and doc index |
| [HUMANS.md](HUMANS.md) | Run and use |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Commits, tests, docs policy |
| [CHANGELOG.md](CHANGELOG.md) | Release notes |
| [SECURITY.md](SECURITY.md) | Vulnerability reporting |
| `archive.go` | Entry point, format switch, extraction policy |
| `walk.go` | The single per-format walk both entry points share |
| `lister.go` | `Lister` over the walk, with no policy |
| `names.go` | Name cleaning and `WindowsSegment` |
| `stream.go` | tar and the single-file compressors |
| `size.go` | `DeclaredSize` |
| `sevenzip_limits.go` | The 7z header walk that bounds decoder memory |
| `errors.go` | Sentinels, `Error`, `wrap` |

## Gate

```bash
gate
```

`gate` detects build, lint, test, vuln and actionlint, and reads `.gate.toml` for the two it cannot
infer: the formatter diff and the Windows cross-compile. The gate's binaries come from the `tool`
directive in `go.mod`, so a linter bump is a diff to `go.mod`.

`go test` must also pass on Windows, which no Linux gate can prove: `WindowsSegment`, the
`0o600`/`0o750` mode assertions, case-collision detection and `os.Root`'s reserved-name handling are
all runtime values there. CI runs a `windows-latest` job for exactly that.

## Comments

Say why, not history. No "previously", no ticket or finding IDs, no benchmark numbers, no record of
a rejected alternative. Those drift, and git already holds them.
