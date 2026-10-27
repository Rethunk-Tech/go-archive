# AGENTS.md

## Shape

Package `archive` reads untrusted archives. It has exactly one consumer, and exports only what that
consumer calls: anything it does not call stays unexported, including both 7z entry points and every
cap the module does not quote in a user-facing message.

The module must never import the donor project it was copied out of, its subpackages, or the
packages that project owns. It is standalone, and is gated with that project nowhere in the loop.

## Gate

Run all of this before committing:

```sh
go tool golangci-lint run && go tool golangci-lint fmt --diff && go vet ./... && go test ./... && go tool govulncheck ./...
```

`go test` must also pass on Windows, which CI covers with a `windows-latest` job running the same
tests. That material cannot run on Linux: `WindowsSegment`, the `0o600`/`0o750` mode assertions,
case-collision detection and `os.Root`'s reserved-name handling are all runtime values there.
Linting, formatting and `govulncheck` are platform-independent and run on Linux only.

The gate's binaries come from the `tool` directive in `go.mod`, through `go tool`. Never install one
globally. CI fetches its own releases, so the `lint-version` input in the workflow mirrors that
directive and has to track it.

## Comments

Say why, not history. No "previously", no ticket or finding IDs, no benchmark figures, and no record
of an alternative that was rejected: those drift, and git already holds them.
