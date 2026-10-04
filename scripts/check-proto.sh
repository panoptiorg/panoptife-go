#!/usr/bin/env bash
# Drift check: the proto/ here is a VENDORED COPY. The canonical definitions
# live in the panopticode core repo. When a sibling checkout is present, diff
# against it; otherwise say so and exit 0 (nothing to compare against).
#
#   panoptife-go/            <- this repo
#   panopticode/proto/       <- canonical
#
# Two differences are BY DESIGN and are normalized away before the diff:
#   - `option go_package` (this repo's Go module path is not panopticode's);
#   - comments (the vendored copy's example strings are sanitized).
# Anything else — a field, a message, a number, an option — is real drift.
set -euo pipefail
cd "$(dirname "$0")/.."
CANON=${CANON:-../panopticode/proto}

if [ ! -d "$CANON" ]; then
  echo "check-proto: skip — no canonical proto dir at $CANON"
  echo "check-proto: vendored at panopticode commit $(cat proto/PROTO_VERSION)"
  exit 0
fi

# strip: go_package option, whole-line comments, trailing comments, blank lines.
norm() {
  sed -E \
    -e '/^[[:space:]]*option[[:space:]]+go_package/d' \
    -e 's://.*::' \
    -e 's:[[:space:]]+$::' \
    "$1" | grep -v '^[[:space:]]*$'
}

rc=0
for f in cgf.proto summary.proto cgstore.proto; do
  if ! diff -u <(norm "$CANON/$f") <(norm "proto/$f"); then
    echo "DRIFT: proto/$f differs from $CANON/$f (ignoring go_package + comments)"
    rc=1
  fi
done

if [ $rc -eq 0 ]; then
  echo "check-proto: OK — proto/ matches $CANON (vendored at $(cat proto/PROTO_VERSION))"
else
  echo "check-proto: re-vendor with:"
  echo "  cp $CANON/*.proto proto/"
  echo "  # restore the go_package lines to github.com/panoptiorg/panoptife-go/..."
  echo "  scripts/genproto.sh"
  echo "  git -C ../panopticode rev-parse --short HEAD > proto/PROTO_VERSION"
fi
exit $rc
