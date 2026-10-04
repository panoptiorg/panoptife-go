# panoptife-go

**The Go frontend for [Panopticode][core]: turns a Go module into code graph facts.**

[![CI](https://github.com/panoptiorg/panoptife-go/actions/workflows/ci.yml/badge.svg)](https://github.com/panoptiorg/panoptife-go/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

`pc-fe` reads a Go module and writes CGF, the input of the
[panopticode][core] taint engine. It records how values move through each
function, which functions each call can reach, and which gRPC and GraphQL
endpoints each function serves or calls. A gRPC client call in one repository
and its handler in another get the same contract id, so the engine can join
them.

**Overview, diagrams and live examples: [panopti.org](https://panopti.org)**

## Quickstart

You need Go 1.25 or newer, and no older than the target module's `go` line.
`fixtures/` holds three small services that call each other: a GraphQL
resolver in `federation` calls `backend` over gRPC, and `backend` calls
`downstream`, which runs SQL.

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
