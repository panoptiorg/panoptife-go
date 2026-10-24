<img src=".github/logo.svg" alt="panoptife-go logo" width="126" height="126">

# panoptife-go

**The Go frontend for [Panopticode][core]: turns a Go module into code graph facts.**

[![CI](https://github.com/panoptiorg/panoptife-go/actions/workflows/ci.yml/badge.svg)](https://github.com/panoptiorg/panoptife-go/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

`pc-fe` reads a Go module and writes CGF, the input of the
[panopticode][core] taint engine. It records how values move through each
function, which functions each call can reach, which gRPC, GraphQL and HTTP
endpoints each function serves or calls, and which Kafka topics it writes and
reads. A gRPC client call in one repository and its handler in another get
the same contract id, an HTTP client request and the route it hits get the
same method and path, and a topic is one cell for every repository, so the
engine can join them.

Supported: gRPC (protoc-gen-go-grpc), GraphQL (gqlgen), HTTP routes on
`net/http` (incl. Go 1.22 patterns), chi v5, gin, echo v4 and gorilla/mux,
`net/http` clients, and Kafka through segmentio/kafka-go, IBM/sarama and
twmb/franz-go. The details and limits are in
[How extraction works](docs/extraction.md).

**Overview, diagrams and live examples: [panopti.org](https://panopti.org)**

## Install

`pc-fe` loads packages through the `go` command, so you need Go anyway. Build
it with the same Go you will run it with:

<!-- x-release-please-start-version -->
```bash
go install github.com/panoptiorg/panoptife-go/cmd/...@v0.3.2   # pc-fe, cgfdump, cgfstat
```

Each [release](https://github.com/panoptiorg/panoptife-go/releases) also has a
tarball per platform (linux and darwin, amd64 and arm64) with `pc-fe`,
`cgfdump` and `cgfstat`, and a `SHA256SUMS` file:

```bash
platform=darwin_arm64   # or darwin_amd64, linux_amd64, linux_arm64
base=https://github.com/panoptiorg/panoptife-go/releases/download/v0.3.2
file=panoptife-go_0.3.2_$platform.tar.gz
curl -fLO $base/$file -O $base/SHA256SUMS
shasum -a 256 -c --ignore-missing SHA256SUMS
tar -xzf $file
```
<!-- x-release-please-end -->

At run time `pc-fe` needs:

- a Go toolchain on `PATH` that is at least as new as the `go` line of the
  module you extract, and no newer than the Go `pc-fe` was built with
  (`pc-fe --version` shows it). A newer one fails with `toolchain skew`:
  rebuild `pc-fe` with `go install`;
- the module's dependencies, downloadable or already in the module cache
  (for private modules set `GOPROXY` and `GOPRIVATE` as for `go build`).

## Quickstart

Building from a checkout needs Go 1.25 or newer.
`fixtures/` holds small services that call each other. Three of them form
the example below: a GraphQL resolver in `federation` calls `backend` over
gRPC, and `backend` calls `downstream`, which runs SQL.

```bash
go build -o bin/pc-fe ./cmd/pc-fe
for r in federation backend downstream; do bin/pc-fe build fixtures/$r --out out/cgf/$r; done
```

To get findings, run the [engine][core] over the three directories. With the
engine built in a sibling checkout:

```console
$ ../panopticode/core/target/debug/panopticode taint --catalog ../panopticode/catalog.example.toml \
    --cgf out/cgf/federation --cgf out/cgf/backend --cgf out/cgf/downstream > chains.json 2> taint.log
$ jq -r '.[] | select(.source_fn | test("accountResolver\\).Archive"))
    | .route.hops[] | [.kind, .repo, .callee // .func] | @tsv' chains.json | column -t
source    example.com/federation  (*example.com/federation/graph.accountResolver).Archive
call      example.com/federation  (*example.com/federation/graph.Account).GetAccountNumber
boundary  example.com/federation  pb.Account/Archive
boundary  example.com/backend     pb.Ledger/Record
sink      example.com/downstream  (*example.com/downstream/store.Storage).Selectx
```

A GraphQL argument crosses two gRPC calls and reaches a SQL query in a third
repository.

## Documentation

| | |
|---|---|
| [CLI](docs/cli.md) | every flag and exit code, plus the `cgfdump` and `cgfstat` debugging tools |
| [How extraction works](docs/extraction.md) | loading, call resolution, value flow, contracts, known gaps |
| [Fixtures](docs/extraction.md#fixtures) | what each example module covers |
| [CGF schema](proto/cgf.proto) | vendored from the engine |

## Contributions 
Are welcome; see
[CONTRIBUTING.md](CONTRIBUTING.md). Licensed under [Apache-2.0](LICENSE).

## Part of Panopticode

| | |
|---|---|
| [panopticode][core] | the engine: joins CGF from many repositories and finds taint flows |
| **panoptife-go** | Go frontend (this repository) |
| [panoptife-ts][ts] | TypeScript and Svelte frontend |

[core]: https://github.com/panoptiorg/panopticode
[ts]: https://github.com/panoptiorg/panoptife-ts
