# Security

## Supported versions

The latest tagged release. There are no long-lived release branches, so a fix ships in the next tag
rather than being backported.

## Reporting a vulnerability

Use GitHub's **private vulnerability reporting** on this repository (Security → Report a
vulnerability). It stays private until a fix ships.

Please do not open a public issue for a suspected vulnerability first.

## What is in scope

This package reads untrusted archives. The interesting failures are ones where crafted input escapes
the guarantees above it:

- An entry that writes outside the `*os.Root` passed to `Extract`, whether through a traversal name,
  a symlink, a hardlink, or a case collision on a case-insensitive filesystem.
- A crafted header that makes a decoder allocate far beyond the 256 MiB decoder window cap,
  including a dictionary or window size declared in the archive itself.
- A crafted archive that makes a reader panic rather than return an error. `RecoverMalformed` turns
  such a panic into `ErrMalformed` for a deferred call, but a reachable panic is still a bug.
- A `Lister` that decompresses past `ListerOptions.MaxEntryBytes`, or reports a size that differs
  from the bytes it yields.
- A ratio or size cap that a crafted archive can satisfy while extracting more than the cap allows.

Format detection is by magic bytes, so a file whose extension disagrees with its contents is read as
what its bytes say. That is intended, and not an issue.

## What is out of scope

Archives the caller has already validated and chosen to trust. This package bounds resource use; it
does not decide what a caller should do with a file it extracted.
