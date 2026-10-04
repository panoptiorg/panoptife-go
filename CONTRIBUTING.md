# Contributing

You need Go 1.25 or newer. A change to flags, output or extraction behaviour
updates [`docs/`](docs/) in the same pull request.

## Build and test

```bash
go build ./... && go vet ./... && go test ./...
OUT=out scripts/extract-fixtures.sh            # every fixture
OUT=out scripts/extract-fixtures.sh dispatch   # one fixture
```

`go test ./...` does not build the [fixtures](docs/extraction.md#fixtures),
because each is its own module. `extract-fixtures.sh` builds `bin/pc-fe` and
`bin/cgfstat`, extracts each fixture twice into `$OUT/<fixture>` (default
`out`, replaced on every run) and fails unless both runs are byte-identical.

## Protobuf

`proto/` is a vendored copy of the engine's canonical `proto/` (`cgf.proto`,
`summary.proto`, `cgstore.proto`). `proto/PROTO_VERSION` records the engine
commit it was copied from.

```bash
CANON=../panopticode/proto scripts/check-proto.sh   # compare proto/ with an engine checkout
scripts/genproto.sh                                 # regenerate internal/{cgfpb,summarypb,cgstorepb}
```

`check-proto.sh` reads `CANON` (default `../panopticode/proto`) and ignores
`option go_package` lines and comments. On drift it prints a diff and the
re-vendoring steps and exits 1. When `CANON` does not exist it prints a skip
line and exits 0.

`genproto.sh` needs `protoc` and `protoc-gen-go` on `PATH`. Use the versions
named in the header of `internal/cgfpb/cgf.pb.go`; then regenerating without
a `.proto` change produces no diff. To re-vendor, copy the engine's `*.proto`
into `proto/`, restore their `go_package` lines, run `scripts/genproto.sh`,
and write the engine's short commit to `proto/PROTO_VERSION`.

## CI

[`ci.yml`](.github/workflows/ci.yml) runs on every push and pull request, on
Ubuntu with Go 1.25: build, vet and test; `go build ./...` in each fixture
module; `scripts/extract-fixtures.sh`; `scripts/check-proto.sh`. CI checks
out no engine, so the drift check skips there; run it locally before changing
`proto/`.
