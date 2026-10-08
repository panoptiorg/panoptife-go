# How pc-fe extracts CGF

This document follows `pc-fe build` from loading a module to writing CGF.
The entry point is `Run` in `internal/emit/emit.go`. Flags are listed in
[cli.md](cli.md); the output schema is [proto/cgf.proto](../proto/cgf.proto).

The CGF for a function is a small value-flow graph plus its call sites. It
does not contain taint results: the [panopticode core][core] computes a
summary per function bottom-up (a functional summary analysis with an SCC
fixpoint over the merged call graph) and composes summaries across
repositories at contract boundaries.

## 1. Loading

`internal/loader` calls `packages.Load` on the `--scope` patterns from the
module directory, with full syntax and type information for the matched
packages and all their dependencies (type-checked from source), and
`Tests: false`, so `_test.go` files are never loaded. Type errors in individual packages are
tolerated. The run fails instead of emitting a near-empty CGF when:

- any package reports `requires newer Go version` (the toolchain `pc-fe` was
  built with is older than the module's `go` directive; rebuild it with a
  newer one);
- nothing was built, or more than half the returned root packages have load
  or type errors;
- a scope pattern that is a literal import path produced no package
  (`--allow-missing-scope` overrides this). Wildcard and relative patterns
  such as `./...` or `./internal/x` are not checked.

SSA is created for the whole import closure, because callers need callee
types, but function bodies are built only for in-scope packages, in parallel.
Dependencies, the standard library and packages outside `--scope` therefore
have no bodies in the CGF, and the core treats calls into them as library
calls.

Mock packages are dropped before SSA building unless `--include-mocks` is
set. A package is a mock when its path has a `--mock-paths` segment (default
`mock`, `mocks`) or a segment starting with `mock_`, its name starts with
`mock_`, or all its non-test files are mock files.

## 2. Which functions are emitted

A function is emitted when it has a body and belongs to an in-scope package,
and its file is not `*_test.go`, `*_mock.go` or `zzz_*` (the last is a
naming convention for generated mocks; the file rules apply even with
`--include-mocks`). Synthetic functions whose declaring package is in scope
are included: method values (`$bound`), method expressions (`$thunk`),
promoted methods of embedded structs, and generic instantiations. Without
them a call through `x.Method` as a value would resolve to nothing.

Generated code is kept, because generated getters and dispatchers carry data.
Each function records its `generator` from the file name (`*.pb.go`,
`*_grpc.pb.go`, `*_vtproto.pb.go` for protoc; `*.generated.go`,
`models_gen.go` for gqlgen) and its `origin`: `user` for the module's own
packages, `stdlib` when the first path segment has no dot, `dep` otherwise.

## 3. Dispatch

`--dispatch vta` (default) builds a whole-program call graph with
`golang.org/x/tools/go/callgraph/vta`; `cha` uses class hierarchy analysis,
which is faster and returns more targets; `off` builds none. A panic inside
the call-graph builder is recovered and treated as no graph. VTA analyses
every in-scope function at once; on a large module, narrow `--scope` or use
`--dispatch cha`.

`internal/callgraph.Resolver` then answers per call instruction. It keeps
only targets that are themselves emitted, removes duplicates and sorts them by
name so the output is stable. A site with one target gets confidence 1.0, a
site with `n` targets gets `1/n`. A site with more than 10 targets
(`DefaultFanoutCap`) is marked `opaque` with no targets: the core then treats
the call as an unknown library call.

A call through a function value with no targets is marked `opaque`; an
interface method call with no targets is not. Either gets one callee id
hashed from its `callee_fqn`, which matches no emitted function. With
`--dispatch off` no interface or function-value call has targets. Both
kinds, and builtin calls, are counted as `unresolved` in the census line
below.

### Call-graph cache (cgstore)

`--cgstore <dir>` stores the resolver's per-site decisions after a successful
write, and a later run at the same commit replays them instead of running
VTA or CHA. The cache is used only when the module is in a git repository
with a clean working tree and `--dispatch` is not `off`; otherwise `pc-fe`
prints `cgstore: disabled (<reason>)` (silently for `off`).

The key covers the module path, scope, a SHA-256 of the `pc-fe` executable,
the Go toolchain version, dispatch mode, fan-out cap, every emission flag,
`--pb-paths`, `--mock-paths`, the `go.sum` hash and the store's schema
version. Snapshots live under `<dir>/<module>/<key>/<commit>/`, and the
newest 8 commits per key are kept. On replay the snapshot is checked against
the freshly built SSA (function set, call-site counts, target ids); any
mismatch prints `cgstore: <reason> — rebuilding` and falls back to a full
build. On the `miniledger` fixture this takes the dispatch step from 4–8 ms
to 1–2 ms.

## 4. LocalFlow

For each function, `internal/flow` turns SSA def-use chains into a graph over
slots and ports:

| vertex kind | meaning |
|---|---|
| `IN_PARAM`, `IN_RECEIVER` | the function's inputs |
| `IN_GLOBAL` | a heap-cell read (`--heap-slots`) or a Kafka topic read (`--topic-cells`) |
| `OUT_RETURN` | a returned value |
| `OUT_PARAM_BYREF`, `OUT_RECEIVER_BYREF` | a parameter or receiver written through a pointer, slice, map or channel (`--byref-out`) |
| `OUT_FIELD` | a heap-cell write (`--heap-slots`) or a Kafka topic write (`--topic-cells`) |
| `CALL_ARG_PORT`, `CALL_RESULT_PORT` | the inputs and outputs of one call site |

Intermediate SSA values are collapsed: an edge means "data can flow from this
vertex to that one inside the function". Calls are barriers. Data enters a
call through its argument ports and leaves through its result ports, and the
core connects the two using the callee's summary.

Field paths (`--field-paths`, on) attach a path of at most two fields to a
vertex, so `req.Number` and `req.Name` are distinct; longer paths are
truncated to their first two fields, which over-approximates. For generated
protobuf structs a field is identified by its proto field number, otherwise
by its index. Trivial generated getters (`req.GetNumber()`) in `--pb-paths`
packages that are in `--scope` (the getter's body must be built) become
field projections and their call sites disappear. Nullable
wrapper types are transparent to field paths: `database/sql` `Null*`,
`guregu/null`, `volatiletech/null`, sqlboiler `types/null` and `wrapperspb`.

Closures (`--closure-flow`, on): free variables become `IN_PARAM` vertices
numbered after the declared parameters, and the `MakeClosure` instruction
gets a synthetic call site, appended after the real ones, that binds them.

Call sites record kind (`STATIC`, `VIRTUAL`, `INVOKES_REMOTE`, `GO`,
`DEFER`), argument and result counts, whether argument 0 is the receiver,
`callee_fqn` for catalog matching, the resolved `callee_iids`, confidence,
the `opaque` bit, and with `--error-results` a bitmask of `error`-typed
results. `BUILTIN` exists in the schema but is not emitted: a builtin call
such as `len` is an `opaque` `STATIC` site whose `callee_fqn` is its
signature.

### Containers

With `--container-writes` (on), `m[k] = v` makes `v` and `k` flow into `m`,
and `ch <- v` (including `select` send cases) makes `v` flow into `ch`. The
container is treated as a whole. When the container itself was loaded from a
field (`s.cache[k] = v`), the write also counts as a store to that field.

### Library write-back

The core has no body for library code, so by default a library call passes
data from its arguments to its results only. A core catalog
`[[propagators]]` rule can say that a call writes into one of its arguments
(`sb.WriteString(s)` into `sb`, `json.Unmarshal(b, &v)` into `v`). With
`--library-writeback` (on), at every call whose callee has no emitted body,
each pointer, slice, map or channel argument port also flows back into the
caller's value, with interface conversions and reslices removed so the flow
reaches `v` and not the `any` it was wrapped in. These edges carry nothing
unless a propagator rule puts data on the port. Pointers packed into a
variadic slice (`rows.Scan(&a, &b)`) are not covered.

### Heap cells

With `--heap-slots` (off), selected struct fields become program-wide cells
identified by type and field. A read is an `IN_GLOBAL` vertex and a write an
`OUT_FIELD` vertex with the same `sym`, and the core joins all writers of a
cell to all its readers. This connects producers and consumers with no call
path between them, typically through channels, at the cost of treating every
instance of a type as one object. `--heap-slots-scope` chooses channel-typed
fields (default) or all fields; channel sends, `select` and `copy` are
modelled too. `--heap-iface-narrow` records the concrete type on both sides
of an interface-typed cell so the core can drop pairings a type assertion
rules out.

### Request field reads

A catalog source rule matches call sites, and `r.Body` is a field load, not a
call. With `--surface-reads` (on), a load of `Body`, `Form`, `PostForm`,
`MultipartForm`, `Header`, `Trailer`, `URL`, `Host` or `RequestURI` of a
`net/http.Request` gets a synthetic call site: `STATIC`, `opaque`, no
arguments, one result, `callee_fqn = "read:net/http.Request.<Field>"`. Its
result flows into the loaded value; the ordinary edge from `r` to the value
stays, so a summary still sees the parameter. This is what lets
`json.NewDecoder(r.Body).Decode(&req)` start a chain: the catalog names
`read:net/http.Request.Body` as a source and the `Decode` propagator moves it
into `req`. The list is a table in `internal/frameworks`. A request this
process builds to send gets no `read:` site: one that comes, within the
function, from `http.NewRequest[WithContext]`, a `&http.Request{}` literal,
the `Request` field of an `*http.Response`, or `Clone`/`WithContext` of
such a request. Every other request (a parameter, a field of one, an
accessor's result, a handler's `r.WithContext(ctx)`) keeps it. The request
object itself is not a source unless `--http-seed-request` is set, because a request
hands out a `context.Context` (`r.Context()`, and gin's `*gin.Context` is one)
that reaches nearly every database and client call.

The synthetic sites are appended after every real call site and after the
closure bindings, so the ids of the real sites, and the call-graph snapshots
keyed on them, do not move.

## 5. Contracts and remote calls

Contracts are what let the core connect repositories. Each has a name and a
`contract_iid` computed the same way in every repository.

### gRPC servers

`internal/contracts` looks for named structs embedding
`Unimplemented<Svc>Server` or `Unsafe<Svc>Server`. The methods of the
generated `<Svc>Server` interface in the same package are the RPC list; if
that interface is missing, `pc-fe` prints `contracts-warn:` and falls back to
matching method shapes. Streaming kind (unary, server, client, bidi) comes
from the generated `<Svc>_<Method>Server` parameter types.

The contract name is `<go package name>.<Svc>/<Method>`, for example
`pb.Account/GetAccount`. It uses the Go package name of the generated code,
not the `package` declared in the `.proto` file, so two repositories join
only if their generated packages have the same Go name.

Each handler gets a `GrpcMethod`, an `Endpoint` with `untrusted_input = true`,
a `binds_to` entry, and `source_params`: the parameters that are pointers to
named structs in a `--pb-paths` package (the request message).

### gRPC clients

A call becomes `INVOKES_REMOTE` with the contract name as `callee_fqn` when
one of these holds, tried in order:

1. it is `Send`, `Recv`, `SendMsg`, `RecvMsg`, `SendAndClose` or
   `CloseAndRecv` on a generated `<Svc>_<Method>Server` or `...Client`
   stream type in a `--pb-paths` package (a stream data port);
2. the receiver is a generated `<Svc>Client` interface in a `--pb-paths`
   package;
3. the receiver is any other interface that exactly one generated
   `<Svc>Client` implements: a hand-written interface narrowing it, or the
   generated interface itself in a package `--pb-paths` does not match
   (`internal/flow/remoteclient.go`). A generated client is recognised by
   `--pb-paths` or by every method taking `...grpc.CallOption`. If more than
   one client matches, the call is left as a normal virtual call and
   reported on a `remote-warn:` line.

Otherwise the call goes through normal dispatch.

### GraphQL (gqlgen)

`internal/graphql` recognises a gqlgen executable schema by a generated
`ResolverRoot` interface next to `DirectiveRoot` and `ComplexityRoot`. No
`gqlgen.yml` is read. Resolver methods have Go names, so `pc-fe` reads the
generated `_<Type>_<field>` functions to recover the field and argument
names exactly as in the SDL. When no such function exists it lowercases the
first letter of the Go name and prints `graphql-warn:`. The contract name is
`<Type>.<field>`; root types are `Query`, `Mutation`, `Subscription` and
`Entity`. Each resolver gets a `GraphqlField`, an `Endpoint`, and
`source_params` for its argument parameters.

### HTTP routes

With `--http-routes` (on), `internal/httproute` reads router registration
calls; a function is a route handler only if something registers it.

| framework | registrations | prefixes | request parameters |
|---|---|---|---|
| `net/http` | `(*ServeMux).Handle`/`HandleFunc`, `http.Handle`/`HandleFunc`, Go 1.22 `"METHOD [host]/path"` patterns | `mux.Handle(p, http.StripPrefix(p, sub))` | `r` → `[1]` |
| chi v5 | `Get`, `Post`, … `Trace`, `Method`, `MethodFunc`, `Handle`, `HandleFunc` | `Route(p, fn)`, `Mount(p, sub)`, `Group(fn)`, `With(…)` | `[1]` |
| gin | `GET`, `POST`, …, `Any`, `Handle`, `Match` on an engine, group or `IRouter`; the last handler of the chain | `Group(p)` | `c` → `[0]` |
| echo v4 | `GET`, `POST`, …, `Any`, `Add`, `Match`; the handler after the path | `Group(p)` | `[0]` |
| gorilla/mux | `HandleFunc`, `Handle`, `(*Route).HandlerFunc`/`Handler`, with `.Methods(…)` before or after | `PathPrefix(p).Subrouter()`, `Path(p)` | `[1]` |

The path of a route is the router's prefix joined with the registered
pattern, the way each framework joins them: gin like `path.Join`
(`Group("/v1").GET("users")` is `/v1/users`), the others as written. An
unknown piece of a prefix is always its own segment (`Route("/api", …)`
then `Route(ver, …)` gives `/api/{}/…`). A `net/http` pattern ending in `/`
serves its whole subtree and becomes a catch-all: `"/static/"` is
`/static/{*}`, a host-only `"example.com/"` is `/{*}`; `"/x/{$}"` is exact.
Prefixes come from a small value analysis: every router value carries a set
of (router, prefix) pairs, at most 8. They start at the constructors
(`chi.NewRouter()`), grow through groups, reach the router parameter of a
`Route` closure, flow into in-scope functions' router parameters through the
call graph (`registerUsers(r)`) and back out through returns, and travel
through local variables, closure captures and struct fields. A router
reached by more than 8 prefixes keeps 8, and its routes are also emitted
under an unknown prefix (their known suffix), counted as unresolved. A
sub-router mounted with chi `Mount` serves its routes under the parent's
full routing prefix plus the mount path. Behind `http.StripPrefix(p, sub)` it
serves them under p plus only what earlier `StripPrefix` mounts removed:
`StripPrefix` strips from the URL path, which still holds any group or
`PathPrefix` the parent routed through. A router registered on another
without `StripPrefix` keeps its own paths, because it sees the full path.
Strings are evaluated as described under [HTTP clients](#http-clients).

A handler argument is resolved through functions, closures, method values
(to the method), `http.HandlerFunc(f)`, a value implementing `http.Handler`
(to its `ServeHTTP`), middleware calls `mw(h)` (to `h`), and one level of
factory call returning a closure. A wrapper's argument is followed only when
it is itself a handler: a `func(http.ResponseWriter, *http.Request)`,
`func(*gin.Context)` or `func(echo.Context) error`, named or not, or an
`http.Handler`. Any other argument (`makeHandler(encodeJSON)`) is a
dependency, and the closure the factory returns is the handler. A factory
that can return different closures does not resolve. A handler that does not
resolve to an emitted function produces no route.

Each route gets an `Endpoint{HTTP}` named `<METHOD> <path as written>` with
`untrusted_input = true`, an `HttpRoute` (method, canonical path, the path as
written, handler, framework, request parameters) in the handler's package, and
a `binds_to` on the handler. The method is upper case; `*` means any method
(no method in a `net/http` pattern, chi `Handle`, gin `Any`, gorilla without
`Methods`). The contract name is `http:<METHOD> <canonical path>`, for example
`http:GET /api/users/{}`, and its id is computed like a gRPC contract id.

The canonical path, shared with the core and the TypeScript frontend: the
scheme and host are dropped, `?query` and `#fragment` cut, empty segments
dropped; a parameter in any syntax (`{id}`, `{id:[0-9]+}`, `:id`, `[id]`) or a
partly dynamic segment becomes `{}`; a catch-all (`{path...}`, `*`, `*name`)
becomes `{*}` and ends the path. `internal/frameworks/canon.go` implements it
and the shared test vectors pin it.

A prefix that cannot be resolved (a router parameter of a function nobody in
the module calls, a prefix read from configuration such as oapi-codegen's
`BaseURL`) is dropped: the route keeps the known suffix and is counted under
`prefixes_unresolved`; the core matches such paths by suffix. A pattern with
no literal part at all is not a route.

### HTTP clients

With `--http-calls` (on), a call to `net/http.Get`, `Head`, `Post`,
`PostForm`, `NewRequest`, `NewRequestWithContext`, or the same methods of
`*http.Client`, gets a second, synthetic call site after the real ones:
`STATIC`, `opaque`, one argument, no result, `callee_fqn = "http:<METHOD>
<path>"`, and `http_call = {method, path}`. Every data-bearing argument (the
URL, the body, the form) flows into its argument 0; the context does not. The
core links the site to a matching route. Unlinked, it has no result to
taint, so it changes nothing. The ordinary call site is kept, so catalog
`egress` rules still apply to it.

The method is the one in the function name or the constant method argument
of `NewRequest`; anything else is `""` (unknown) in `http_call`, and `*` in
the site's `callee_fqn`. The URL is evaluated by a
bounded string evaluator: constants, `+`, `fmt.Sprintf` with a constant
format (each verb is a hole), `url.JoinPath`, `path.Join`, a `Phi` whose
inputs agree, and a local variable assigned once; anything else is a hole.
Every value is evaluated once per evaluation, and an evaluation stops after
4096 steps with a hole that the census lines count as `eval_budget`. An
unresolved start of the URL (a base from configuration) stays one leading
`{}` segment, so `baseURL + "/api/users"` becomes `/{}/api/users`, a path
known only as a suffix. A literal scheme and host are dropped.

### Kafka topics

With `--topic-cells` (on), a Kafka topic is a heap cell like the struct-field
cells of `--heap-slots`, keyed by `ContractIID("msg:kafka:<topic>")` and named
`kafka topic <topic>`, so the core joins a producer in one repository to a
consumer in another. It does not need `--heap-slots`.

| library | produce | consume | topic from |
|---|---|---|---|
| segmentio/kafka-go | `(*Writer).WriteMessages` | `(*Reader).ReadMessage`, `FetchMessage` | `Writer{Topic}` or `NewWriter(WriterConfig{Topic})`, else each `Message{Topic}`; `NewReader(ReaderConfig{Topic, GroupTopics})` |
| IBM/sarama, Shopify/sarama | `SyncProducer.SendMessage`, `SendMessages`, a send on `AsyncProducer.Input()` | `ConsumerGroup.Consume(ctx, topics, handler)` binding the handler's `ConsumeClaim`; `PartitionConsumer.Messages()` | `ProducerMessage{Topic}`; the `topics` slice; `ConsumePartition(topic, …)` |
| twmb/franz-go | `(*Client).Produce`, `TryProduce`, `ProduceSync` | `(*Client).PollFetches`, `PollRecords`, and the callback of `Fetches.EachRecord` | `Record{Topic}`, else `kgo.DefaultProduceTopic(t)`; `kgo.ConsumeTopics(…)` at `kgo.NewClient` |

A cell holds message payloads: `Value` of a kafka-go `Message`, a sarama
`ProducerMessage` or `ConsumerMessage`, and a franz-go `Record` (one table in
`internal/frameworks`). A produce is an `OUT_FIELD` vertex with the topic's
`sym` that the payload flows into: the payload a local message literal
stores, or the payload field of a message from elsewhere. A consume is an
`IN_GLOBAL` vertex with the same `sym` whose value is injected into the
received message at its payload field. The message is the call's result,
each value received from the returned channel (`<-ch`, `range`, a `select`
case), or the first parameter of an `EachRecord` callback. So `m.Value`
reads the cell, `m.Topic`, `m.Key` or `m.Offset` do not, and a helper given
`m` sees the cell at the payload path. A franz-go `PollFetches` batch has no
payload field of its own and is the cell's value as a whole. A sarama
`ConsumeClaim` bound by `Consume` also gets an `Endpoint{MESSAGE}` named
`kafka topic <topic>` and a `binds_to`.

A topic resolves from a constant (and the string operations above), a field
of a struct literal built in the same function, or `os.Getenv("NAME")`,
which becomes the symbol `${env:NAME}` (`os.Getenv("PFX") + "-orders"` is
`${env:PFX}-orders`): two services configured by the same variable name
join, and a different name does not. A Kafka topic name cannot contain `$`,
`{` or `}`, so a symbol never collides with a literal topic. Anything else
is no cell; there is no wildcard topic. A kafka-go writer that names a topic
decides it (kafka-go rejects a message topic next to it); otherwise each
message's own topic field is used, then the handle's: a franz-go
`Record.Topic` overrides `DefaultProduceTopic`. The census line splits the unresolved sites:
`from_param` (the topic is a parameter of the enclosing function, an
in-house wrapper), `from_config` (a field of a non-library struct), `other`
(anything else, including a writer or reader built in another function and
kept in a struct field).

## 6. Identity

- `iid` = SHA-256 over the length-prefixed repository, package path,
  function name and signature. It survives body edits.
- **Generic instances** (and closures nested in them) are named and hashed
  from their type arguments' *identity*: aliases resolved at every depth,
  parameter names dropped, interfaces flattened to their method sets
  (`--canonical-instance-ids`, default on). `go/ssa` creates one instance per
  identity class, for whichever caller asks first, and packages build in
  parallel; the instance's printed name takes that caller's spelling
  (`lo.Contains[model.Str]` vs `lo.Contains[string]` for an alias), and its
  signature is shared with every identical instantiated signature, parameter
  names included (`slices.Contains[[]string string]` and `lo.Contains[string]`).
  Hashing those strings made the same commit yield different iids run to run.
  The same canonical form is used for value types inside an instance's body,
  so its `bid` is stable too. Ordinary functions are unaffected; an instance
  with no alias, no named parameter in a function-typed type argument and no
  interface type argument keeps exactly `go/ssa`'s name.
- `bid` = SHA-256 over the canonical LocalFlow and the sorted callee iids.
  The canonical form drops spans, field names and callee ids from the flow,
  so moving a line or renaming a field does not change it (except a heap-cell
  field under `--heap-slots`, whose `sym_name` is kept). Callees enter by
  `iid`, so a change inside a callee does not change its callers' `bid`.
- `contract_iid` = the same hash over the contract name
  (`graphql:<Type>.<field>` for GraphQL, `http:<METHOD> <path>` for an HTTP
  route); a Kafka topic cell's `sym` is the same hash over
  `msg:kafka:<topic>`.

The core caches summaries by `bid`, so output must be deterministic:
functions and packages are sorted, and each file is marshalled with
deterministic protobuf encoding. `scripts/extract-fixtures.sh` checks this on
every fixture; see [CONTRIBUTING.md](../CONTRIBUTING.md).

## 7. Output

One file per package with emitted functions or contracts, written to `--out`
(see [cli.md](cli.md#output)). With `--require-contracts` and nothing found,
the files are written first and the run then exits 3. The cgstore snapshot and
the `mr` manifest are written after the CGF.

## Warnings

Every run prints to stderr:

- `pc-fe timing: load=... ssa-build=... dispatch[<mode>]=...` (`<mode>-cached`
  when replayed from cgstore);
- `opaque: sites= opaque= capped= unresolved= dangling=`: total call sites,
  opaque sites, sites over the fan-out cap, interface, function-value and
  builtin calls with no targets, and resolved targets outside the emitted set;
- with `--http-calls`, `http-calls: N sites, resolved_path=, dynamic_base=,
  unknown_method=, eval_budget=`: synthetic client sites, those whose path
  has a literal segment, those whose URL starts with an unresolved base,
  those with no constant method, and those whose URL or method exhausted the
  evaluator's step budget;
- with `--topic-cells`, `topics: produce=N (resolved, from_param, from_config,
  other) consume=M (…) cells=C`: produce and consume sites (a sarama
  `Consume` binding counts as one consume site, an `EachRecord` callback
  none), how many resolved, why the rest did not, the number of distinct
  topics, and the evaluations that exhausted the step budget;
- with `--http-routes`, `http-routes: N routes (<framework>=<n> …)
  handlers_unresolved= prefixes_unresolved=`: routes, registrations whose
  handler did not resolve, registrations under an unresolved prefix (or more
  prefixes than are kept) or with no literal path, and registrations whose
  path or prefix exhausted the evaluator's step budget.

Printed when they apply:

| prefix | meaning |
|---|---|
| `emit-warn: dangling callee iid` | a call target is not in the emitted set; the core will treat it as a library call |
| `emit-warn: ... Unimplemented<Svc>Server but 0 gRPC handlers` | the server type exists but its handlers are outside `--scope` |
| `contracts-warn:` | the generated `<Svc>Server` interface was not found; handlers were matched by shape |
| `graphql-warn:` | a GraphQL name was derived from the Go method name |
| `remote-clients:` | calls linked to a generated client by rule 3 of [gRPC clients](#grpc-clients) |
| `remote-warn:` | gRPC-shaped calls on interfaces that no generated client, or more than one, implements |
| `generics-gap:` | generic instantiations and call edges that could not be emitted |
| `cgstore:` | cache disabled, rebuilt after a failed replay, or failed to write |

When no contract and no remote call was found, a multi-line hint lists the
supported generators and the current `--pb-paths`.

## Known gaps

- Recognised boundaries: `protoc-gen-go-grpc`, gqlgen, the five HTTP routers
  and `net/http` clients above, and the three Kafka libraries. connect-go,
  twirp, drpc, OpenAPI generators (oapi-codegen, ogen, go-swagger), fiber,
  httprouter, resty and go-retryablehttp produce no facts.
- HTTP: routes registered in a loop over a table, through a project's own
  registration helper whose path is a parameter, or on a router whose
  constructor is not in the module are not recovered (the last keeps its
  suffix). Middleware is not modelled: a route behind an authentication
  middleware is reported like any other. gorilla `Host`, `Queries` and
  `Headers` matchers are ignored, and `PathPrefix(p).Handler(h)` is a route
  at `p`, not a catch-all. A route registered through a project's own
  router interface (not the library's) is not seen. Only the request
  direction is modelled: response data does not flow back to the client.
- HTTP clients: URLs built with `strings.Builder`, by a helper function, from
  a package variable or a `url.URL` struct are holes; a method that is not a
  constant is unknown.
- Kafka: no wrapper-parameter forwarding, no handle built in another
  function, no config-derived topics; one cell per topic merges every
  message type on it; a regular expression in `ConsumeTopics` is taken as a
  literal topic name; the `Shopify/sarama` path is assumed to have the IBM
  API. One sarama handler type passed to several `Consume` calls reads the
  union of their topics: its `ConsumeClaim` is one function, so per-call
  topics cannot be represented. A literal written twice into a writer's
  `Topic` field yields both cells. Records reached from a franz-go
  `PollFetches` batch through `Records()` or `RecordIter()` carry the whole
  batch's taint, not only `Value`.
- `Send`/`Recv` on a generated stream type whose package is not matched by
  `--pb-paths` is not recognised as a stream port. Calls on the generated
  `<Svc>Client` itself are still caught by rule 3 of
  [gRPC clients](#grpc-clients).
- Contract names depend on the Go package name of generated code.
- Scope holes behind wildcard patterns are not detected; code outside the
  scope has no body.
- Sites with more than 10 dispatch targets are opaque.
- Pointers inside variadic arguments to library calls do not receive writes.
- `--heap-slots` is object-insensitive: all instances of a type share a cell.

## Fixtures

Each directory under `fixtures/` holding a `go.mod` is its own module
(`example.com/<name>`), so `go test ./...` does not build it; `kafka/producer`
and `kafka/consumer` are a pair one level deeper. CI extracts all of them with
`scripts/extract-fixtures.sh`; the core uses the extracted CGF as golden test
data. Tests in `internal/callgraph` (`dispatch`), `internal/httproute`
(`httpapi`), `internal/flow` (`httpclient`) and `internal/emit` (`httpapi`,
`httpclient`, `kafka/*`) read fixtures directly. `httpapi` and the Kafka pair
depend on the real router and Kafka libraries at pinned versions, so loading
them needs the module proxy or a filled module cache.

Besides the lines every run prints ([Warnings](#warnings)), extracting
`federation` prints `remote-clients:` and `graphql-warn:`, and `backend`
prints `contracts-warn:`. These are expected: the fixtures exercise the
fallbacks on purpose.

| fixture | covers |
|---|---|
| `federation` | gqlgen resolvers calling gRPC: unary, streaming, a hand-written client interface, and a resolver with no generated dispatcher (`graphql-warn`) |
| `backend` | gRPC server with unary and streaming handlers, one forwarding to `downstream`; the missing `FeedServer` interface triggers `contracts-warn` |
| `downstream` | the last service in the `federation` → `backend` → `downstream` chain; runs SQL |
| `dispatch` | interface calls with 2 targets and with 12 (over the fan-out cap) |
| `fieldpath` | field paths keeping disjoint request fields apart |
| `closureflow` | closure free variables |
| `wrappers` | method values, method expressions and promoted methods |
| `heapslots` | `--heap-slots` producer and consumer joined through a channel field |
| `byrefout` | `--byref-out` parameter and receiver out-slots |
| `errorleaf` | `--error-results` with the core's `--no-error-leaf` |
| `containers` | map writes and channel sends |
| `libwrites` | library write-back into arguments (`strings.Builder`, `json.Unmarshal`-style calls) |
| `miniledger` | one service struct with many interface fields, a step runner and an event pool; dispatch-induced recursion |
| `recursion` | recursion shapes: self-recursion, recursion through a return value, mutual recursion, a diamond, and interface dispatch to two implementations |
| `httpapi` | one API on five routers mounted on one `ServeMux`: Go 1.22 patterns, chi `Route`/`Group`/`Mount`/`With` and a router passed to a function, gin groups and `ShouldBindJSON`, gorilla subrouter and `*Route` methods, echo; `r.Body` into SQL, and a handler passing only `ctx` to a constant query |
| `httpclient` | `net/http` client calls with a configured base URL, `fmt.Sprintf`, `url.JoinPath`, a constant URL and an unknown method; calls `httpapi` |
| `kafka/producer`, `kafka/consumer` | a kafka-go writer and a sarama consumer group joined by topic `orders`, a decoy pair `audit-events` / `audit-log` that must not join; also sarama's async `Input()`, franz-go `DefaultProduceTopic` and `ConsumeTopics`, a kafka-go reader, an `os.Getenv` topic and a parameter topic |

`federation`, `backend` and `downstream`, with panoptife-ts's `webapp`, form
the example system; [The example system](https://github.com/panoptiorg/panopticode/blob/master/docs/example.md)
maps their code to the 18 findings they produce.

[core]: https://github.com/panoptiorg/panopticode
