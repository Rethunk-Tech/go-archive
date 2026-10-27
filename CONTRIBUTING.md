# Contributing

Read [`AGENTS.md`](AGENTS.md) first.

## Commits

`type(scope): subject` — imperative, about 72 characters, one logical unit, why in the body, no AI
trailers.

## Gate

```bash
gate
```

Everything CI runs has a local counterpart, so a green `gate` and a green run mean the same thing.
The one exception is the `windows-latest` job: its behaviour cannot run on Linux, so it is only ever
green in CI.

## Exports

The module has one consumer. A new export needs a call site in that consumer, not a use for it here.
Tests are in the same package and exercise unexported code directly, so they are not a reason to
export anything.

## Dependencies

A new dependency needs a reason the standard library cannot meet. This module reads untrusted input,
so the decoder implementations it relies on are part of its security surface.

## Changelog

[`CHANGELOG.md`](CHANGELOG.md): Keep a Changelog, newest first. Add to Unreleased in the same commit
as a behaviour change. Refactors, tests and docs do not.

Tag `vX.Y.Z` to cut a release.
