#!/bin/sh
# Regenerates testdata/many-entries.7z: DefaultMaxEntries+1 empty files, the
# smallest 7z that is over the default entry cap. Needs 7z or 7zz.
set -eu
out=$(cd "$(dirname "$0")" && pwd)/many-entries.7z
sz=$(command -v 7zz || command -v 7z)
work=$(mktemp -d "${TMPDIR:-/var/tmp}/many-entries.XXXXXX")
trap 'rm -rf "$work"' EXIT
(cd "$work" && seq 1 20001 | sed 's/^/e/' | xargs touch)
rm -f "$out"
(cd "$work" && "$sz" a -mx=9 -bso0 -bsp0 "$out" ./* >/dev/null)
