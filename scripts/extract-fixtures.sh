#!/usr/bin/env bash
# Extract every Go fixture module with pc-fe and assert the extraction is
# deterministic: a second run over the same tree must be byte-identical.
#
# Silent nondeterminism (map iteration order leaking into the CGF bytes) does
# not break correctness — it breaks summary reuse, because a body identity
# (bid) that changes for no reason cold-starts the core's cache on every run.
# Only a double-extract diff catches it.
#
#   OUT=out scripts/extract-fixtures.sh          # all fixtures
#   OUT=out scripts/extract-fixtures.sh dispatch # one fixture
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT=$(pwd)
OUT=${OUT:-out}
mkdir -p "$OUT"

echo "== build =="
go build -o bin/pc-fe ./cmd/pc-fe
go build -o bin/cgfstat ./cmd/cgfstat
FE="$ROOT/bin/pc-fe"
STAT="$ROOT/bin/cgfstat"

if [ $# -gt 0 ]; then
  FIXTURES=("$@")
else
  FIXTURES=()
  for d in fixtures/*/; do
    [ -f "$d/go.mod" ] || continue
    FIXTURES+=("$(basename "$d")")
  done
fi

fail=0
for fx in "${FIXTURES[@]}"; do
  echo "== $fx =="
  rm -rf "$OUT/$fx" "$OUT/$fx.again"
  "$FE" build "fixtures/$fx" --scope './...' --out "$OUT/$fx"
  "$FE" build "fixtures/$fx" --scope './...' --out "$OUT/$fx.again"
  if diff -r "$OUT/$fx" "$OUT/$fx.again" > /dev/null; then
    echo "  determinism: OK (re-extract byte-identical)"
  else
    echo "  determinism: FAIL — re-extract is not byte-identical"
    diff -r "$OUT/$fx" "$OUT/$fx.again" || true
    fail=1
  fi
  rm -rf "$OUT/$fx.again"
  # sed, not head: head exits after one line, cgfstat takes SIGPIPE on the
  # rest, and under pipefail that fails the run (exit 141) whenever cgfstat
  # is still writing — a timing race, not a result. sed reads to EOF.
  "$STAT" "$OUT/$fx" | sed -n 1p
done

if [ "$fail" -ne 0 ]; then
  echo "FAIL: extraction is not deterministic"
  exit 1
fi
echo "PASS: ${#FIXTURES[@]} fixture(s) extracted, all re-extracts byte-identical"
