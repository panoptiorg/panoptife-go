# Command-line reference

This repository builds three commands: `pc-fe` (the extractor), and two
tools for inspecting its output, `cgfdump` and `cgfstat`. How extraction
works is described in [extraction.md](extraction.md).

## pc-fe build

```text
pc-fe build <module-dir> [flags]
```

Takes exactly one argument, the directory of a Go module (the one holding
`go.mod`). There is no config file.

### Output

One `.pb` file per Go package that has at least one emitted function or
contract, named after the package path with `/`, `\` and `:` replaced by `_`
(`example.com_backend_app.pb`). Each file is a deterministic binary
`CgfPackage` message from [proto/cgf.proto](../proto/cgf.proto) with
`language = "go"` and `schema_version = 2`. `commit_sha` is the `HEAD` of the
git repository containing the module, or empty outside git.

The output directory is created if needed but never emptied. Files from an
earlier run whose package no longer exists remain, and the core loads every
`*.pb` in the directory. Extract into a new or emptied directory.

On success `pc-fe` prints one line to stdout:

```text
repo=example.com/backend commit=22817b1b packages=3 functions=23 out=out/cgf/backend
```

In `--mode mr` a line `mr base=<sha> head=<sha> changed_files=<n> changed_fns=<n>`
precedes it. Everything else goes to stderr: a `pc-fe timing:` line, the
`opaque:` census, and the warnings listed in
[extraction.md](extraction.md#warnings).

### Flags

Boolean flags take `--flag` or `--flag=false`. A flag that defaults to on is
turned off only with `=false` (`--field-paths=false`); `--field-paths false`
fails, because `false` is read as a second argument.

Input and output:

| flag | default | meaning |
|---|---|---|
| `--scope` | `./...` | comma-separated `go/packages` patterns, relative to the module dir. Only these packages are analysed |
| `--out` | `<module-dir>/../output/cgf/<module path, sanitized>` | output directory; see above |
| `-v`, `--verbose` | `false` | print per-package load errors, one `wrote <file> (<n> functions)` line per file, and the cgstore write line |
| `--allow-missing-scope` | `false` | continue when a `--scope` pattern that is a literal import path loaded nothing. Wildcard and relative patterns (`./...`, `./x`) are never checked |
| `--include-mocks` | `false` | build and emit mock packages instead of dropping them. Files named `*_mock.go` or `zzz_*` are excluded either way |
| `--mock-paths` | `mock,mocks` | path segments that mark a mock package. Each entry must be a single segment |
| `--pb-paths` | `pb,api` | path segments that mark a generated protobuf package: the import path contains `/<seg>/` or ends in `/<seg>`. Each entry must be a single segment. Controls remote-call recognition, stream ports, getter canonicalisation and which handler parameters are untrusted |
| `--require-contracts` | `false` | exit 3 when no gRPC method, GraphQL field or remote call site was extracted. The CGF is written first |

`--pb-paths` and `--mock-paths` replace their default list. To add a segment,
repeat the defaults: `--pb-paths=pb,api,gen,proto`.

Merge-request mode:

| flag | default | meaning |
|---|---|---|
| `--mode` | `main` | `main` or `mr`. `mr` also writes `manifest.json` to `--out` |
| `--base`, `--head` | none | commits to diff in `mr` mode; both required there |

In `mr` mode the working tree is extracted as usual; `manifest.json` lists the
files changed between `--base` and `--head` (`git diff`) and the emitted
functions declared in them: `{mode, base, head, changed_files[], changed_fns[{iid, bid, fqn, file}]}`.
`pc-fe` warns if the checked-out `HEAD` is not `--head`. The core ignores the
manifest when loading CGF.

Call graph:

| flag | default | meaning |
|---|---|---|
| `--dispatch` | `vta` | `vta` (variable type analysis, precise, whole-program), `cha` (class hierarchy analysis, faster, more targets), or `off` (no call graph: interface and function-value calls get no targets) |
| `--cgstore` | empty | directory for call-graph snapshots. Empty or `off` disables it. See [extraction.md](extraction.md#call-graph-cache-cgstore) |

Value-flow emission. Defaults reproduce the CGF the core's tests expect;
every one of these changes the CGF bytes and is part of the cgstore key.

| flag | default | meaning |
|---|---|---|
| `--field-paths` | `true` | field paths up to depth 2 on flow vertices (`req.Number` rather than `req`); trivial protobuf getters become field projections |
| `--closure-flow` | `true` | closure free variables become extra parameters, bound where the closure is created |
| `--container-writes` | `true` | `m[k] = v` and `ch <- v` flow into the map or channel |
| `--library-writeback` | `true` | at a call with no emitted body, pointer, slice, map and channel arguments can receive the call's writes (used by the core's `[[propagators]]` catalog rules) |
| `--heap-slots` | `false` | struct fields become program-wide heap cells joining writers and readers; also channel sends, `select` and `copy` |
| `--heap-slots-scope` | `chan` | with `--heap-slots`: `chan` (channel-typed fields only) or `all` |
| `--heap-iface-narrow` | `false` | with `--heap-slots`: tag interface-typed cells with the concrete type so the core can drop impossible pairings |
| `--heap-iface-drop` | `false` | with `--heap-slots`: never create cells for interface-typed fields. Experimental; loses real flows |
| `--byref-out` | `false` | out-slots for parameters and receivers written through a pointer, plus the caller-side back-edge |
| `--error-results` | `false` | record which call results are `error`-typed (`CallSite.error_results`), for the core's `--no-error-leaf` |
| `--error-results-strict` | `false` | with `--error-results`: also drop the whole-tuple alias for multi-result calls |

### Exit codes

| code | when |
|---|---|
| 0 | success, including a run with no contracts when `--require-contracts` is not set, and a failed cgstore write |
| 1 | any error: wrong argument count, invalid flag value, `--mode mr` without `--base`/`--head`, load failure or a load guard (toolchain skew, most packages failing to type-check, missing scope package), write failure, `git diff` failure |
| 3 | `--require-contracts` and nothing was found |

Errors are printed once to stderr as `error: <message>`.

### Environment

`pc-fe` reads no environment variables itself. `go/packages` runs the `go`
command, so `GOFLAGS`, `GOTOOLCHAIN`, `GOWORK`, `GOPROXY`, `CGO_ENABLED` and
the other Go variables apply as they would to `go build`. `git` is run, when
available, for `rev-parse`, `status` (cgstore) and `diff` (`mr` mode).

## cgfdump

```text
cgfdump [-flow <substring>] <file.pb>
cgfdump -sites <file.pb | dir>
```

Decodes one CGF file and prints each function and its call sites:

```text
FN (*example.com/backend/app.Implementation).GetAccount iid=6fc27fbafc52 src_params=[1] binds=1
  cs=0 kind=STATIC callee=(*example.com/backend/store.Storage).FindAccountByNumber n=1 opaque=false conf=1.0000 argc=2 iids=[ 1f166ff79d11]
```

`iid` is truncated to 12 hex digits, `src_params` are the untrusted
parameter indices, `binds` counts the contracts the function implements.

| flag | default | meaning |
|---|---|---|
| `-flow` | empty | also print flow vertices and edges for functions whose name contains the substring |
| `-sites` | `false` | one tab-separated row per call site instead: function, call-site id, opaque, confidence, comma-separated full callee iids. Accepts a directory |

It does not print the package's contract lists (`grpc_methods`,
`graphql_fields`, `endpoints`). Exit codes: 0 on success, 2 on a bad flag
or an unreadable or undecodable file (reported as a Go panic).

## cgfstat

```text
cgfstat <cgf-dir> [<cgf-dir>...]
```

Prints call-site statistics per directory:

```text
out/cgf/backend: shards=3 fns=23 sites=19 opaque=2
  fan-out (len CalleeIids → sites):
      1 → 19
  dispatch_confidence → sites:
    1.0000 → 19
```

No flags. Exit codes: 0 on success, 1 on an unreadable or undecodable file,
2 with no arguments. A directory with no `.pb` files (or one that does not
exist) prints zero counts and exits 0.

## Scripts

The development scripts in `scripts/` are described in
[CONTRIBUTING.md](../CONTRIBUTING.md).
