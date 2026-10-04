#!/usr/bin/env bash
# Regenerate the Go protobuf stubs from the vendored proto/*.proto.
# Requires protoc + protoc-gen-go on PATH. Output is byte-stable for a given
# (protoc-gen-go, .proto) pair, so a regeneration with no .proto change must
# produce no diff — check that before committing.
set -euo pipefail
cd "$(dirname "$0")/.."
protoc -I proto \
  --go_out=. --go_opt=module=github.com/panoptiorg/panoptife-go \
  proto/cgf.proto proto/summary.proto proto/cgstore.proto
echo "regenerated internal/{cgfpb,summarypb,cgstorepb}"
