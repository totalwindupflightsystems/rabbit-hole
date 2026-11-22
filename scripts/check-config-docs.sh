#!/usr/bin/env bash
# Compare production environment reads with the Configuration table.
set -eu
export LC_ALL=C
cd "$(dirname "$0")/.."
tmp=$(mktemp -d "${TMPDIR:-/tmp}/config-docs.XXXXXXXX")
trap 'rm -rf "$tmp"' EXIT

# Include direct reads throughout production code and any key constants in
# the config package. Ignore tests and comments, not specific key names.
find internal cmd -type f -name '*.go' ! -name '*_test.go' -print > "$tmp/files"
while IFS= read -r file; do
    grep -E '^[[:space:]]*[^/].*(Getenv|LookupEnv|envOr|getenv)[[:space:]]*\(' "$file" | grep -oE '(Getenv|LookupEnv|envOr|getenv)[[:space:]]*\([[:space:]]*"[A-Z][A-Z0-9_]*"' || :
done < "$tmp/files" > "$tmp/reads"
# Constants can be passed to accessors instead of a string literal.
grep -Eh '^[[:space:]]*([[:alnum:]_]+[[:space:]]+)?[[:alnum:]_]+[[:space:]]*=[[:space:]]*"RABBITHOLE_[A-Z0-9_]+"' internal/config/*.go >> "$tmp/reads" || :
grep -oE '"[A-Z][A-Z0-9_]*"' "$tmp/reads" | tr -d '"' | sort -u > "$tmp/scanned" || :
awk '/^## Configuration$/ { section=1; next } section && /^## / { exit } section && /^\| `[A-Z][A-Z0-9_]*` \|/ { print $2 }' docs/integration.md | tr -d '`' | sort -u > "$tmp/documented"
if [ ! -s "$tmp/scanned" ] || [ ! -s "$tmp/documented" ]; then
    echo 'error: no config reads or configuration table rows found' >&2
    exit 1
fi
comm -23 "$tmp/scanned" "$tmp/documented" > "$tmp/missing"
printf 'scanned=%s documented=%s missing=%s\n' "$(wc -l < "$tmp/scanned" | tr -d ' ')" "$(wc -l < "$tmp/documented" | tr -d ' ')" "$(wc -l < "$tmp/missing" | tr -d ' ')"
if [ -s "$tmp/missing" ]; then
    echo 'Missing keys:' >&2
    cat "$tmp/missing" >&2
    exit 1
fi
