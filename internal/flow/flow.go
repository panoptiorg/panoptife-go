// Package flow builds the per-function LocalFlow value-flow sidecar from SSA
// def-use (doc 02 §6, plan "Frontend↔core contract"). Calls are BARRIERS: taint
// enters a call only via its arg-ports and leaves only via its result-ports, so
// the Rust core supplies the through-call summary edge. Boolean taint => the
// distributive transfer means intraprocedural reachability == the IFDS fixpoint,
// so this collapse is lossless at slot granularity.
//
// One deliberate extension of the slot vocabulary (W1d): a closure's captured
// variables are emitted as IN_PARAM vertices at the param indices just past the
// declared ones, and bound at the *ssa.MakeClosure site by a synthetic CallSite
// appended after all real ones. See assignParams / emitClosureBindings.
//
// A second (W1F, doc 24 N5): a struct field is an abstract HEAP CELL with a
// program-wide identity `sym = H(type, field)`. A read of the cell is an
// IN_GLOBAL vertex, a write is an OUT_FIELD vertex carrying the same sym, and
// the Rust side joins writers to readers in a phase-2 fixpoint (core/heap.rs).
// The join is object-INsensitive by construction — every *Pool shares one
// `(Pool, incoming)` cell — which is what lets a producer and a consumer that
// share no call path (debt A1: `Submit` ⇢ `runBatcher`) reach each other at
// all. See heapHook.
package flow

import (
	"go/token"
	"go/types"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ssa"

	"github.com/panoptiorg/panoptife-go/internal/callgraph"
	pb "github.com/panoptiorg/panoptife-go/internal/cgfpb"
	"github.com/panoptiorg/panoptife-go/internal/hash"
	"github.com/panoptiorg/panoptife-go/internal/pkgclass"
)

// Callee describes the target(s) of one call site so emit can resolve iids.
type Callee struct {
	FQN            string
	Static         *ssa.Function   // resolved static callee, or nil
	Targets        []*ssa.Function // VTA targets for virtual dispatch
	Kind           pb.CallSite_Kind
	RemoteFullName string // for INVOKES_REMOTE: "pkg.Service/Method"
}

// Result is the LocalFlow plus per-callsite callee resolution (index-aligned
// with Flow.Callsites).
type Result struct {
	Flow    *pb.LocalFlow
	Callees []Callee
	// NCallInstrs is how many of Flow.Callsites are backed by a real
	// ssa.CallInstruction — always a PREFIX, ids 0..NCallInstrs-1. The tail is
	// the synthetic closure-binding sites, which `*ssa.MakeClosure` is NOT a
	// CallInstruction for. cgstore's snapshot ordinal key
	// (callgraph.callInstructions) re-enumerates CallInstructions, so it must
	// validate against THIS count, never len(Flow.Callsites).
	NCallInstrs int
	// CappedSites is the count of dynamic-dispatch call sites (interface
	// invoke or func-value call) where Dispatcher.TargetsAt reported
	// cappedOpaque — fan-out over the resolver's cap, reported opaque rather
	// than enumerated. Census-only (pc-fe's "opaque:" stderr line); not part
	// of the CGF.
	CappedSites int
	// UnresolvedDynamicSites is the count of call sites with no static callee
	// where TargetsAt resolved zero targets WITHOUT being capped — the resolver
	// found no callee at all (e.g. dispatch=off, or a genuinely unindexed
	// site). Builtins such as `len` take this path too. Distinct from
	// CappedSites: a function-value or builtin call gets cs.Opaque=true, an
	// interface call does not. Census-only.
	UnresolvedDynamicSites int
}

// Dispatcher resolves virtual-dispatch targets per call site: the live
// callgraph.Resolver, or a cgstore snapshot replay (R4 Ph1). Implementations
// must treat a nil receiver / nil site as "resolves nothing".
type Dispatcher interface {
	TargetsAt(site ssa.CallInstruction) (targets []*ssa.Function, confidence float32, cappedOpaque bool)
}

// Opts selects the emission features. Every field is an escape-hatch flag whose
// FALSE value reproduces the previous emission byte-for-byte, so a corpus A/B
// is attributable to exactly one change.
type Opts struct {
	// FieldPaths: k=2 field-path facts (doc 20 §2).
	FieldPaths bool
	// ClosureFlow: closure free-variable slots + MakeClosure binding sites (W1d).
	ClosureFlow bool
	// HeapSlots: heap-cell in/out vertices, *ssa.Send, precise *ssa.Select and
	// the `copy` builtin (W1F, doc 24 N5). All four ride ONE flag deliberately —
	// the A1 chain needs every one of them, so splitting them would produce an
	// acceptance test that cannot fire (doc 28 §1b).
	HeapSlots bool
	// HeapAllFields widens heap cells from channel-typed fields (the A1 shape,
	// ~46 cells on ledger-svc) to every struct field (~thousands). Measurement
	// instrument for doc 28; the wide setting is what A1 actually needs, because
	// `pendingBatch.requests` is a slice field.
	HeapAllFields bool
	// HeapIfaceNarrow tags interface-typed heap cells with the concrete type on
	// each side of the join — the reader's `*ssa.TypeAssert` and the writer's
	// `*ssa.MakeInterface` — so `heap::fixpoint` can drop a pairing whose writer
	// cannot satisfy the assertion (A16, doc 30 §6.1). Ignored unless HeapSlots.
	// Default OFF: an emission change, so OFF must reproduce the previous CGF
	// byte-for-byte or the A/B is not attributable.
	HeapIfaceNarrow bool
	// HeapIfaceDrop is the BLUNT filter — never open a cell for an
	// interface-typed field at all. **Measurement instrument only.** doc 30 §7b
	// sized it and rejected it as a shipping candidate: it happens to cost ~3
	// true-positive keys on today's five corpora but it disables 20-31% of the
	// sink-carrying cell population, which is a property of these commits and not
	// of the rule. It exists so the precise narrowing can be A/B'd against it.
	HeapIfaceDrop bool
	// ByRefOut: OUT_PARAM_BYREF / OUT_RECEIVER_BYREF out-slots for by-ref
	// params and receivers, plus the caller-side back-edge that makes them
	// carry (W1a, doc 29). Both halves ride one flag: without the back-edge the
	// out-slot is inert (doc 29 §1c), so shipping them apart would produce an
	// acceptance test that cannot fire — the W1F lesson (doc 28 §1b).
	ByRefOut bool
	// ErrorResults: CallSite.error_results, the bit-per-result-index mask of
	// `error`-typed results (B1, doc 31 §4). The core's default leaf refuses to
	// taint those ports — the largest FP class measured in this tree (doc 30
	// §6.2, 214-278 keys). Result-port vertices carry no type (see :585), so the
	// core cannot derive this; a zero mask means "unknown" and keeps today's
	// behaviour. Default OFF: an emission change, so OFF must reproduce the
	// previous CGF byte-for-byte.
	ErrorResults bool
	// ErrorResultsStrict additionally stops registering the CALL VALUE (the
	// result tuple) as a source for multi-result calls, so port 0 no longer
	// aliases it (doc 31 §6a). Without this the error filter reaches only
	// 1-result producers: the tuple's walk lands port 0 on the consumers of
	// `err` too. Measured trade on 5 repos — +79 FP keys removed, 2 REAL
	// backend-a keys lost, because the tuple walk builds path variants
	// (`[0][ID]`) the per-port walks do not. Hence its own flag: the recall cost
	// is real and must stay attributable. Requires ErrorResults.
	ErrorResultsStrict bool
	// ContainerWrites: `m[k] = v` (*ssa.MapUpdate) and `ch <- v` (*ssa.Send,
	// and the send cases of *ssa.Select) put v INTO the map / channel, so a
	// later `m[k2]`, `range m` or `<-ch` reads it. Neither instruction is an
	// ssa.Value, so the generic arm (wireOperands) drops both outright and a
	// value written into a map or sent on a channel reaches nothing — even when
	// the read is two lines later in the same function. This is the same
	// weak-update, whole-container edge a store through `&s[i]` already gets
	// (the Store arm's IndexAddr case): strictly local, not a program-wide
	// join, which is why it defaults ON where --heap-slots does not. Map KEYS
	// flow in too — `for k := range m` reads them back, and the dedup-set idiom
	// (`seen[id] = struct{}{}` then ranging the keys into an IN (...) clause)
	// carries request data in nothing else. See containerWrite.
	ContainerWrites bool
	// LibraryWriteback: at a call into LIBRARY code (a callee with no emitted
	// body — stdlib, dependencies, anything out of scope), a by-ref argument's
	// arg port also flows back into the caller's value. The core's default leaf
	// sends a library call's input to its RESULTS only, so `sb.WriteString(s)`
	// filling sb, or `json.Unmarshal(b, &v)` filling v, reached nothing; a
	// catalog `[[propagators]]` rule now tells the core which port a library
	// call writes, and this edge is how that write reaches the rest of the
	// caller. Inert on its own: the core puts taint on these ports only where a
	// rule (or `--unmodeled`) says so. The same back-edge --byref-out adds for
	// every call, restricted to the calls no summary can ever cover.
	LibraryWriteback bool
	// Emittable reports whether a function's body is emitted (emit.go's scope
	// policy) — what makes a callee "library" for LibraryWriteback. nil: fall
	// back to "has no SSA body", which is what out-of-scope means for
	// non-generic callees. Not part of any cache key (a policy, not a setting).
	Emittable func(*ssa.Function) bool
	// RemoteClients links interface calls (hand-rolled client interfaces, or a
	// generated <Svc>Client outside --pb-paths) to a generated <Svc>Client by
	// method-set identity (FN-01; see remoteclient.go). nil = the
	// name-convention detector only. Not part of any cache key: extract_key
	// hashes the binary, so a detector change invalidates by construction.
	RemoteClients *RemoteClientIndex
	// PbPaths is the generated-protobuf package heuristic (--pb-paths). The
	// ZERO VALUE is the historical `pb,api` rule, so every existing
	// zero-valued Opts keeps its emission. It gates remote-call recognition,
	// stream ports and pb-getter canonicalization at once, so it rides in the
	// cgstore key (Config.PbPaths).
	PbPaths pkgclass.PbPaths
}

type builder struct {
	fn    *ssa.Function
	res   Dispatcher
	opts  Opts
	flow  *pb.LocalFlow
	nextV uint32

	// *ssa.MakeClosure sites, in fn.Blocks/Instrs order — their synthetic
	// binding CallSites are appended AFTER every real one (see
	// emitClosureBindings).
	closures []*ssa.MakeClosure
	// number of CallSites backed by a real ssa.CallInstruction.
	nCallInstrs int
	// census-only dynamic-dispatch counters — see Result.CappedSites /
	// Result.UnresolvedDynamicSites.
	nCapped     int
	nUnresolved int

	// value-flow adjacency over ssa.Values (call-barriered)
	succ map[ssa.Value][]edge
	// role maps: an ssa.Value may map to several vertices
	srcVerts  map[ssa.Value][]uint32 // source-side: params, receiver, call results
	sinkVerts map[ssa.Value][]uint32 // sink-side: call args, returns
	// (call value, result index) -> result-port vertex id
	resultPort map[[2]interface{}]uint32
	// source-side (value, base vertex) pairs in creation order — the
	// deterministic iteration base for the accessed-path vocabulary walk
	ordered []srcEntry
	callees []Callee
}

type srcEntry struct {
	val ssa.Value
	vid uint32
}

// value-flow edge ops (doc 20 §2.2): plain copies the fact's field path,
// proj consumes its head, inject prepends (k-truncated).
const (
	opPlain uint8 = iota
	opProj
	opInject
)

type edge struct {
	to    ssa.Value
	alias bool
	op    uint8
	field uint32 // proj/inject: field identity (proto number if tagged, else index)
	name  string // proj/inject: field name (reporting only)
}

// maxFieldPath is k in doc 20 §2 — longer paths collapse to their k-prefix
// ("this subtree", sound over-approx).
const maxFieldPath = 2

// Build computes the LocalFlow for fn. fn must have a body. res resolves
// virtual dispatch per call site; nil res resolves nothing (function-value
// calls opaque, interface calls with no targets but not opaque).
// Each Opts field defaults (false) to the emission that preceded it, byte for
// byte — see Opts.
func Build(fn *ssa.Function, res Dispatcher, o Opts) *Result {
	if res == nil {
		// normalize to a typed nil whose TargetsAt is nil-receiver safe, so the
		// unconditional b.res.TargetsAt calls below never hit a nil interface.
		res = (*callgraph.Resolver)(nil)
	}
	b := &builder{
		fn:         fn,
		res:        res,
		opts:       o,
		flow:       &pb.LocalFlow{},
		succ:       map[ssa.Value][]edge{},
		srcVerts:   map[ssa.Value][]uint32{},
		sinkVerts:  map[ssa.Value][]uint32{},
		resultPort: map[[2]interface{}]uint32{},
	}
	b.assignParams()
	b.emitByRefOuts()
	b.scanInstrs()
	b.emitClosureBindings()
	b.wireExtracts()
	if o.FieldPaths {
		b.emitAccessVariants()
	}
	b.computeEdges()
	// computeEdges iterates the srcVerts MAP, so emission order is randomized;
	// bid hashes the marshaled flow — sort for run-to-run determinism (the
	// summary cache keys on bid, so a flapping edge order = a lost cache hit).
	sort.Slice(b.flow.Edges, func(i, j int) bool {
		a, c := b.flow.Edges[i], b.flow.Edges[j]
		if a.From != c.From {
			return a.From < c.From
		}
		if a.To != c.To {
			return a.To < c.To
		}
		return !a.ViaAlias && c.ViaAlias
	})
	return &Result{
		Flow:                   b.flow,
		Callees:                b.callees,
		NCallInstrs:            b.nCallInstrs,
		CappedSites:            b.nCapped,
		UnresolvedDynamicSites: b.nUnresolved,
	}
}

func (b *builder) addVertex(kind pb.VertexKind, index, callsite uint32, typ string, span *pb.Span) uint32 {
	id := b.nextV
	b.nextV++
	b.flow.Vertices = append(b.flow.Vertices, &pb.FlowVertex{
		Id:         id,
		Kind:       kind,
		Index:      index,
		CallsiteId: callsite,
		Type:       typ,
		Span:       span,
	})
	return id
}

func (b *builder) assignParams() {
	hasRecv := b.fn.Signature.Recv() != nil
	paramIdx := uint32(0)
	for i, p := range b.fn.Params {
		if i == 0 && hasRecv {
			v := b.addVertex(pb.VertexKind_IN_RECEIVER, 0, 0, p.Type().String(), nil)
			b.srcVerts[p] = append(b.srcVerts[p], v)
			b.ordered = append(b.ordered, srcEntry{p, v})
			continue
		}
		v := b.addVertex(pb.VertexKind_IN_PARAM, paramIdx, 0, p.Type().String(), nil)
		b.srcVerts[p] = append(b.srcVerts[p], v)
		b.ordered = append(b.ordered, srcEntry{p, v})
		paramIdx++
	}
	if !b.opts.ClosureFlow {
		return
	}
	// Closure captures (W1d, doc 24 N4: `fn.FreeVars` was never referenced, so a
	// value the closure needs from its creation scope entered it nowhere and the
	// `withTx(…, func(){ Queryx(ctx, query) })` idiom lost `query` outright —
	// doc 26 §5).
	//
	// A capture is an ordinary in-slot, so it needs no new Slot and no core
	// change: FreeVar i is IN_PARAM at index paramIdx+i, i.e. the free vars
	// occupy the param indices just past the declared ones. The two index spaces
	// cannot collide — a real call site only ever supplies ports
	// 0..paramIdx-1 — and the binding side (emitClosureBindings) writes the
	// SAME indices, so the core's existing arg-port -> Param(k) remap wires them
	// up untouched. `Signature.params` deliberately does NOT list them: it
	// mirrors the Go signature, and no consumer indexes it by slot.
	//
	// Only anonymous functions and the `$bound` wrapper have free vars, and
	// neither has a receiver — the `hasRecv` shift above is a no-op for them.
	// The subtraction is kept anyway so the two numberings can never drift.
	for i, fv := range b.fn.FreeVars {
		v := b.addVertex(pb.VertexKind_IN_PARAM, paramIdx+uint32(i), 0, fv.Type().String(), nil)
		b.srcVerts[fv] = append(b.srcVerts[fv], v)
		b.ordered = append(b.ordered, srcEntry{fv, v})
	}
}

// emitByRefOuts gives every by-ref-eligible param and receiver an out-slot
// (W1a, doc 29). `func f(dst *T, src *T) { dst.F = src.F }` has no return and
// no field write the caller can see, so without this the callee's whole effect
// is invisible and the flow dies at the call — doc 24 N4's third missing fact
// kind.
//
// UNCONDITIONAL by design, not restricted to params this function writes
// through. The restricted predicate is 9.2x smaller on ledger-svc (3,348 of
// 30,888) and unsound: `func f(dst *T) { g(dst) }` writes nothing locally, and
// there are 63,047 such pass-on sites — a sound version would need a transitive
// fixpoint over summaries for a fact the core composes by itself (doc 29 §1d).
// The price is an identity flow param_i -> byref_i, since the param's own
// IN_PARAM vertex is a srcVert of the value this vertex sinks. At the caller
// that re-taints an argument that was already tainted, so it is inert.
//
// A vertex only, no `b.ordered` entry: the path variants that matter are
// created on the WRITE side (`addInject` at a FieldAddr store already records
// the path), and walking every by-ref param through emitAccessVariants is the
// O(n^2) shape doc 28 §2 keeps out of the heap reads.
func (b *builder) emitByRefOuts() {
	if !b.opts.ByRefOut {
		return
	}
	hasRecv := b.fn.Signature != nil && b.fn.Signature.Recv() != nil
	paramIdx := uint32(0)
	for i, p := range b.fn.Params {
		recv := i == 0 && hasRecv
		if !b.isByRefType(p.Type()) {
			if !recv {
				paramIdx++
			}
			continue
		}
		kind, idx := pb.VertexKind_OUT_PARAM_BYREF, paramIdx
		if recv {
			// NOT OUT_PARAM_BYREF(0): that index is IN_PARAM-relative and the
			// caller shifts it past the receiver (N3), so a receiver write
			// would land on the first real argument. doc 29 §1b.
			kind, idx = pb.VertexKind_OUT_RECEIVER_BYREF, 0
		}
		v := b.addVertex(kind, idx, 0, p.Type().String(), nil)
		b.sinkVerts[p] = append(b.sinkVerts[p], v)
		if !recv {
			paramIdx++
		}
	}
}

// isByRefType mirrors emit.isByRef (emit.go, `Param.by_ref`): only a pointer,
// slice or map lets a callee's write reach its caller — plus a channel under
// ContainerWrites. A callee's `ch <- v` is a write through a reference exactly
// like `m[k] = v`, and `go produce(ch, x); <-ch` is the idiom that needs the
// out-slot. Without ContainerWrites no send reaches the channel, so the slot
// would carry nothing; gating it keeps the option's OFF emission identical
// under --byref-out too. emit.isByRef is signature metadata the core never
// reads, and widening it would change every CGF, so it is left alone.
func (b *builder) isByRefType(t types.Type) bool {
	switch t.Underlying().(type) {
	case *types.Pointer, *types.Slice, *types.Map:
		return true
	case *types.Chan:
		return b.opts.ContainerWrites
	}
	return false
}

// emitClosureBindings materializes the capture edge at the MakeClosure site: a
// synthetic CallSite whose callee is the closure and whose arg ports carry the
// bound values into the free-var in-slots assignParams just created.
//
// Two ordering constraints, both load-bearing:
//
//   - `*ssa.MakeClosure` is NOT an `ssa.CallInstruction`, and cgstore's snapshot
//     replay keys persisted dispatch on the ORDINAL of a CallInstruction
//     (callgraph.callInstructions). Synthetic sites are therefore appended after
//     every real one, so ids 0..NCallInstrs-1 keep meaning what they meant, and
//     the count the snapshot validates is NCallInstrs (emit.go), never
//     len(Callsites).
//   - resultc is 0: the closure's returns are delivered where it is CALLED, not
//     where it is created. Result ports here would attribute the callee's return
//     taint to the wrong site — and the default leaf (ifds.rs) would smear it
//     over them whenever the closure has no summary.
//
// The site is emitted even when the closure is not an emittable function (an
// out-of-scope `$bound`, e.g. on strings.Builder): no summary ⇒ default leaf ⇒
// resultc 0 ⇒ inert. Keeping emit's scope policy out of flow is worth the dead
// callsite.
func (b *builder) emitClosureBindings() {
	b.nCallInstrs = len(b.flow.Callsites)
	if !b.opts.ClosureFlow {
		return
	}
	for _, mc := range b.closures {
		fn, ok := mc.Fn.(*ssa.Function)
		if !ok || len(mc.Bindings) == 0 {
			continue
		}
		base := uint32(len(fn.Params))
		if fn.Signature != nil && fn.Signature.Recv() != nil {
			base-- // mirror assignParams: IN_PARAM numbering excludes the receiver
		}
		// A method-value MakeClosure carries the selector position; a func
		// literal's is unset, so fall back to the literal's own `func` token.
		// Without this the binding hop renders with no file/line, and a route
		// hop that cannot be opened against source is what doc 24 N2 calls
		// worse than no route.
		span := b.span(mc)
		if span == nil {
			span = b.posSpan(fn.Pos())
		}
		csID := uint32(len(b.flow.Callsites))
		for i, bind := range mc.Bindings {
			v := b.addVertex(pb.VertexKind_CALL_ARG_PORT, base+uint32(i), csID, bind.Type().String(), nil)
			b.sinkVerts[bind] = append(b.sinkVerts[bind], v)
		}
		b.flow.Callsites = append(b.flow.Callsites, &pb.CallSite{
			Id:                 csID,
			Kind:               pb.CallSite_STATIC,
			DispatchConfidence: 1.0,
			CalleeFqn:          fn.String(),
			Argc:               base + uint32(len(mc.Bindings)),
			Resultc:            0,
			Span:               span,
		})
		b.callees = append(b.callees, Callee{FQN: fn.String(), Static: fn, Kind: pb.CallSite_STATIC})
	}
}

func (b *builder) scanInstrs() {
	for _, blk := range b.fn.Blocks {
		for _, instr := range blk.Instrs {
			// W1F heap cells. Deliberately BEFORE the switch and additive-only:
			// with --heap-slots off heapHook returns immediately, so the switch
			// below is the sole emitter and the tree is byte-identical to d6fdacd.
			b.heapHook(instr)
			switch it := instr.(type) {
			case *ssa.Call:
				b.handleCall(it, pb.CallSite_STATIC)
			case *ssa.Go:
				b.handleCall(it, pb.CallSite_GO)
			case *ssa.Defer:
				b.handleCall(it, pb.CallSite_DEFER)
			case *ssa.Store:
				b.storeInto(it.Val, it.Addr)
			case *ssa.MapUpdate:
				if b.opts.ContainerWrites {
					b.containerWrite(it.Value, it.Map)
					b.containerWrite(it.Key, it.Map)
				}
			case *ssa.Send:
				if b.opts.ContainerWrites {
					b.containerWrite(it.X, it.Chan)
				}
			case *ssa.FieldAddr:
				// &x.f: x's field f subtree flows into the address value
				if fid, name, ok := b.projField(it.X.Type(), it.Field); ok {
					b.addProj(it.X, it, fid, name)
				} else {
					b.addFlow(it.X, it, false) // unresolvable ⇒ whole-object (sound)
				}
			case *ssa.Field:
				if fid, name, ok := b.projField(it.X.Type(), it.Field); ok {
					b.addProj(it.X, it, fid, name)
				} else {
					b.addFlow(it.X, it, false)
				}
			case *ssa.MakeClosure:
				// Collect for emitClosureBindings (ordering: see there), then
				// wire exactly what the `default` arm would have — the closure
				// VALUE must stay taint-carrying, or handing it to an opaque
				// callee would lose the capture that used to smear through.
				b.closures = append(b.closures, it)
				for _, op := range it.Operands(nil) {
					if op != nil && *op != nil {
						b.addFlow(*op, it, false)
					}
				}
			case *ssa.Return:
				for j, r := range it.Results {
					v := b.addVertex(pb.VertexKind_OUT_RETURN, uint32(j), 0, r.Type().String(), b.span(it))
					b.sinkVerts[r] = append(b.sinkVerts[r], v)
				}
			case *ssa.Select:
				// N7. The generic arm below wires EVERY operand — all channels
				// *and* all sent values — into the select tuple, so a value sent
				// in one case cross-taints a recv result in another, and the sent
				// value never reaches its channel. With heap cells on we can do
				// better: only recv channels feed the tuple, and heapHook has
				// already routed each sent value into its channel's cell. Smear
				// BETWEEN recv cases remains — the tuple is one value — but that is
				// coarseness, not the send/recv confusion N7 names.
				// ContainerWrites adds what neither arm has: the sent value
				// reaching its OWN channel, so a later `<-ch` sees it.
				if b.opts.ContainerWrites {
					for _, st := range it.States {
						if st.Dir == types.SendOnly && st.Send != nil {
							b.containerWrite(st.Send, st.Chan)
						}
					}
				}
				if !b.opts.HeapSlots {
					b.wireOperands(instr)
					break
				}
				for _, st := range it.States {
					if st.Dir == types.RecvOnly {
						b.addFlow(st.Chan, it, false)
					}
				}
			default:
				b.wireOperands(instr)
			}
		}
	}
}

// handleCall creates a CallSite + its arg/result ports (the barrier).
// instr is the concrete *ssa.Call/Go/Defer; its Value() is the *ssa.Call for
// ordinary calls and nil for go/defer (which produce no results).
func (b *builder) handleCall(instr ssa.CallInstruction, kind pb.CallSite_Kind) {
	cc := instr.Common()
	resultVal := instr.Value()

	// Getter canonicalization (doc 20 §2.2.1): a trivial pb getter is a field
	// projection, not a barrier — keeping the CallSite would re-admit
	// whole-object taint through the getter's default leaf.
	if b.opts.FieldPaths && resultVal != nil {
		if fid, name, ok := trivialPbGetter(cc, b.opts.PbPaths); ok {
			b.addProj(cc.Args[0], resultVal, fid, name)
			return
		}
	}
	// `copy(dst, src)` (W1F). A builtin has no static callee, so it becomes an
	// opaque call site whose default leaf taints results only — and copy's only
	// result is the element count. The effect is entirely on dst, so the barrier
	// loses it. This is edge #4 of the A1 chain (flushOneBatch:213).
	if isCopyBuiltin(cc) && b.opts.HeapSlots {
		b.addFlow(cc.Args[1], cc.Args[0], true)
		return // no CallSite — mirrored by CanonicalizedCallFilter
	}
	csID := uint32(len(b.flow.Callsites))
	cs := &pb.CallSite{Id: csID, Kind: kind, DispatchConfidence: 1.0}

	var args []ssa.Value
	arg0Recv := false
	callee := Callee{Kind: kind}

	if cc.IsInvoke() {
		// interface dispatch: receiver is cc.Value, then cc.Args
		arg0Recv = true
		args = append([]ssa.Value{cc.Value}, cc.Args...)
		callee.Kind = pb.CallSite_VIRTUAL
		cs.Kind = pb.CallSite_VIRTUAL
		if cc.Method != nil {
			callee.FQN = cc.Method.FullName()
		}
		// A Send/Recv on a generated stream interface is a contract data port;
		// a call on a generated <Svc>Client interface is a remote gRPC call.
		if full, op, client, ok := streamPort(cc, b.opts.PbPaths); ok {
			cs.Kind = pb.CallSite_INVOKES_REMOTE
			cs.StreamOp = op
			cs.StreamClientSide = client
			callee.Kind = pb.CallSite_INVOKES_REMOTE
			callee.RemoteFullName = full
			callee.FQN = full
		} else if full, ok := remoteContract(cc, b.opts.PbPaths); ok {
			cs.Kind = pb.CallSite_INVOKES_REMOTE
			callee.Kind = pb.CallSite_INVOKES_REMOTE
			callee.RemoteFullName = full
			callee.FQN = full
		} else if full, ok := b.opts.RemoteClients.Resolve(cc); ok {
			// FN-01: a local interface narrowed over a pb client. Same
			// contract string as the pb type would have produced, so the
			// server side links identically.
			cs.Kind = pb.CallSite_INVOKES_REMOTE
			callee.Kind = pb.CallSite_INVOKES_REMOTE
			callee.RemoteFullName = full
			callee.FQN = full
		} else {
			targets, conf, capped := b.res.TargetsAt(instr)
			callee.Targets = targets
			cs.DispatchConfidence = conf
			switch {
			case capped:
				cs.Opaque = true
				b.nCapped++
			case len(targets) == 0:
				b.nUnresolved++
			}
		}
	} else {
		args = cc.Args
		if sc := cc.StaticCallee(); sc != nil {
			callee.Static = sc
			callee.FQN = sc.String()
			arg0Recv = sc.Signature.Recv() != nil
		} else {
			// dynamic func value: resolve via the call graph if possible
			callee.FQN = cc.Value.Type().String()
			targets, conf, capped := b.res.TargetsAt(instr)
			callee.Targets = targets
			cs.DispatchConfidence = conf
			cs.Opaque = capped || len(targets) == 0
			switch {
			case capped:
				b.nCapped++
			case len(targets) == 0:
				b.nUnresolved++
			}
		}
	}

	// Library call (LibraryWriteback): no target the core will have a summary
	// for, and not a remote contract (those compose through contract views).
	// Builtins are excluded: they reach the CGF named only by their signature
	// (`func([]string, ...string) []string`), so no catalog rule can select
	// one, and `append` — by far the commonest — already flows through its
	// result, which the default leaf covers.
	library := false
	_, builtin := cc.Value.(*ssa.Builtin)
	if b.opts.LibraryWriteback && callee.Kind != pb.CallSite_INVOKES_REMOTE && !builtin {
		if callee.Static != nil {
			library = !b.emittable(callee.Static)
		} else {
			library = len(callee.Targets) == 0
		}
	}

	cs.CalleeFqn = callee.FQN
	cs.Argc = uint32(len(args))
	cs.Arg0IsReceiver = arg0Recv
	cs.Opaque = cs.Opaque || (callee.Static == nil && len(callee.Targets) == 0 && !cc.IsInvoke())
	cs.Span = b.span(instr)

	// arg ports
	for i, a := range args {
		v := b.addVertex(pb.VertexKind_CALL_ARG_PORT, uint32(i), csID, a.Type().String(), nil)
		b.sinkVerts[a] = append(b.sinkVerts[a], v)
		// W1a: the caller-side back-edge. An arg port is sink-only otherwise, so
		// `propagate` deposits a callee's by-ref out-fact on this vertex and then
		// finds no out-edge — the fact dies on arrival (doc 29 §1c). Making the
		// port a SOURCE for the argument value is what carries the mutation into
		// the rest of the caller. Restricted to by-ref-eligible args: a callee
		// cannot write back through anything else, and each srcVerts entry costs
		// one BFS in computeEdges.
		if b.opts.ByRefOut && b.isByRefType(a.Type()) {
			b.srcVerts[a] = append(b.srcVerts[a], v)
			// …and into `ordered`, or the back-edge carries nothing: a
			// whole-object source is CUT at the first projection
			// (`pathState.step`, opProj with plen==0), because the projected
			// paths are supposed to be covered by the path-variant vertices
			// emitAccessVariants materializes from `ordered`. The callee writes
			// `dst.F`, so the caller reads `q.F` — every real case is a
			// projection, and without the variant the fact dies on the first
			// field read. Measured, not assumed: with the variant absent the
			// fixture produced 0 chains.
			b.ordered = append(b.ordered, srcEntry{a, v})
		} else if library {
			// LibraryWriteback. The port writes back into the value the call
			// can actually mutate: `json.Unmarshal(b, &v)` passes `&v` boxed in
			// an `any`, so the MakeInterface is peeled to reach `&v`, whose
			// other uses are what must see the write. Same `ordered` entry as
			// the ByRefOut branch above, for the same reason (the caller reads
			// `v.F`, a projection).
			if t := writebackTarget(a); b.isByRefType(t.Type()) {
				b.srcVerts[t] = append(b.srcVerts[t], v)
				b.ordered = append(b.ordered, srcEntry{t, v})
			}
		}
	}
	// result ports
	resultc := uint32(0)
	if resultVal != nil {
		resultc = uint32(numResults(cc))
		if resultc == 0 {
			resultc = 1 // single unnamed result surfaced as the Call value
		}
		// Port 0 doubles as the alias for the whole Call value, and
		// `computeEdges` walks each SOURCE VALUE — so the tuple's walk runs
		// through every *ssa.Extract and lands port 0 on the consumers of `err`
		// as much as of the value, which is why the core's per-port error filter
		// reaches only 1-result producers by default (doc 31 §6a).
		//
		// --error-results-strict drops that registration for multi-result calls.
		// `wireExtracts` still gives extract i the port-i vertex, so the per-port
		// edges all survive; what is lost is port0 -> consumers of extract j>=1
		// AND the path variants the tuple walk builds (`[0][ID]`) — measured at
		// 2 real backend-a keys against +79 FP keys removed.
		aliasTuple := !b.opts.ErrorResultsStrict || resultc < 2
		for j := uint32(0); j < resultc; j++ {
			v := b.addVertex(pb.VertexKind_CALL_RESULT_PORT, j, csID, "", nil)
			// port 0 aliases the Call value; Extract(call,i) refines per-index below
			if j == 0 && aliasTuple {
				b.srcVerts[resultVal] = append(b.srcVerts[resultVal], v)
				b.ordered = append(b.ordered, srcEntry{resultVal, v})
			}
			// remember index->vertex for Extract wiring
			b.resultPort[[2]interface{}{resultVal, j}] = v
		}
	}
	cs.Resultc = resultc
	if b.opts.ErrorResults {
		cs.ErrorResults = errorResultMask(cc, resultc)
	}

	b.flow.Callsites = append(b.flow.Callsites, cs)
	b.callees = append(b.callees, callee)
}

// emittable: does fn's body reach the core (so it gets a summary)?
func (b *builder) emittable(fn *ssa.Function) bool {
	if b.opts.Emittable != nil {
		return b.opts.Emittable(fn)
	}
	return len(fn.Blocks) > 0
}

// writebackTarget peels the conversions Go inserts when an argument is passed
// to a wider parameter type — boxing into an interface, a named-type change, a
// reslice — down to the value a callee writing through the argument mutates.
// Bounded: these chains are 1-2 deep in practice.
func writebackTarget(a ssa.Value) ssa.Value {
	for i := 0; i < 8; i++ {
		switch v := a.(type) {
		case *ssa.MakeInterface:
			a = v.X
		case *ssa.ChangeInterface:
			a = v.X
		case *ssa.ChangeType:
			a = v.X
		case *ssa.Slice:
			a = v.X
		default:
			return a
		}
	}
	return a
}

func (b *builder) addFlow(from, to ssa.Value, alias bool) {
	b.succ[from] = append(b.succ[from], edge{to: to, alias: alias})
}

func (b *builder) addProj(from, to ssa.Value, field uint32, name string) {
	b.succ[from] = append(b.succ[from], edge{to: to, op: opProj, field: field, name: name})
}

func (b *builder) addInject(from, to ssa.Value, field uint32, name string) {
	// alias: heap-derived, same confidence discount as the old aggregate smear
	b.succ[from] = append(b.succ[from], edge{to: to, alias: true, op: opInject, field: field, name: name})
}

// storeInto is `*addr = val`: the value flows into the address, so a later
// Load(addr) reads it. A store through &agg.field or &arr[i] also taints the
// containing aggregate, so reads of the aggregate (incl. slice/convert, e.g.
// variadic packing) see it. With field paths on, the FieldAddr store is an
// inject (weak update: facts are only ever added, via_alias keeps the
// confidence discount); indexes stay smashed (doc 20 §2.2.5).
func (b *builder) storeInto(val, addr ssa.Value) {
	b.addFlow(val, addr, true)
	switch a := addr.(type) {
	case *ssa.FieldAddr:
		if fid, name, ok := b.projField(a.X.Type(), a.Field); ok {
			b.addInject(val, a.X, fid, name)
		} else {
			b.addFlow(val, a.X, true)
		}
	case *ssa.IndexAddr:
		b.addFlow(val, a.X, true)
	}
}

// containerWrite: val is written INTO the map or channel c (ContainerWrites).
// Every read — Lookup, Range/Next, `<-ch` — already has c as an operand, so the
// edge val -> c is all a read in this function needs to see it. Whole-container
// and weak, exactly like the IndexAddr smear in storeInto: a write under one key
// taints every key's read.
//
// c is often not the container's only SSA name. `s.cache[k] = v` writes
// through `*(&s.cache)`, and the read two lines later is a DIFFERENT load of
// the same address (x/tools SSA does no CSE), so an edge onto the first load
// reaches nothing. Walking back to the address and treating the write as a
// store there reuses storeInto's reach: other loads of that address, the
// containing struct's field path, and — under --byref-out — the receiver or
// param's out-slot. Bounded: these chains are 1-2 deep in practice.
func (b *builder) containerWrite(val, c ssa.Value) {
	for i := 0; i < 8; i++ {
		b.addFlow(val, c, true)
		switch v := c.(type) {
		case *ssa.UnOp:
			if v.Op == token.MUL {
				b.storeInto(val, v.X)
			}
			return
		case *ssa.Field:
			// a map held in a struct VALUE: the write is visible through the
			// struct, at that field
			if fid, name, ok := b.projField(v.X.Type(), v.Field); ok {
				b.addInject(val, v.X, fid, name)
			} else {
				b.addFlow(val, v.X, true)
			}
			return
		case *ssa.Lookup:
			c = v.X // `mm[a][b] = val`: the inner map is reached through the outer
		case *ssa.ChangeType:
			c = v.X
		default:
			return
		}
	}
}

// wireOperands is the generic value-defining rule: every operand flows into the
// defined value. Factored out so the *ssa.Select arm can fall back to it
// verbatim when --heap-slots is off — the OFF tree must be byte-identical, and
// "identical" has to mean the same code, not a copy that drifts.
func (b *builder) wireOperands(instr ssa.Instruction) {
	val, ok := instr.(ssa.Value)
	if !ok {
		return
	}
	for _, op := range instr.Operands(nil) {
		if op != nil && *op != nil {
			b.addFlow(*op, val, false)
		}
	}
}

// ---------------------------------------------------------------------------
// W1F — abstract heap cells (doc 24 N5, doc 28)
// ---------------------------------------------------------------------------

// heapSym is the program-wide identity of one struct field: `sym = H("", pkg,
// TypeName, "field:<fid>")`. Deliberately object-INsensitive (all *Pool values
// share one cell) and deliberately generic-INsensitive: types.Named.Obj() is
// the ORIGIN's TypeName, so `Pool[T,R].incoming` and
// `Pool[EventWithAux,struct{}].incoming` are the same cell — which they must
// be, because the CGF holds both the generic and the instantiated function and
// the producer/consumer pair lands on different ones.
//
// fid is fieldID's rename-stable identity (proto field number when tagged),
// so a cell survives a field rename exactly as a field path does.
//
// iface reports that the field's static type is an interface — the shape A16
// narrows (doc 30 §6.1). It is returned rather than recomputed by the callers
// because this is the only place that has resolved the struct.
func (b *builder) heapSym(typ types.Type, field int) (sym []byte, name string, iface, ok bool) {
	if !b.opts.HeapSlots {
		return nil, "", false, false
	}
	st, named := structOf(typ)
	if st == nil || named == nil || field < 0 || field >= st.NumFields() {
		return nil, "", false, false
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return nil, "", false, false // builtin/unnamed: no stable program-wide identity
	}
	ftyp := st.Field(field).Type()
	if !b.opts.HeapAllFields && !isChanField(ftyp) {
		return nil, "", false, false
	}
	iface = types.IsInterface(ftyp)
	if iface && b.opts.HeapIfaceDrop {
		return nil, "", false, false // the blunt filter — instrument only, see Opts
	}
	fid, fname := fieldID(st, field)
	pkgPath := obj.Pkg().Path()
	sym = hash.IIDFromParts("", pkgPath, obj.Name(), "field:"+strconv.FormatUint(uint64(fid), 10))
	return sym, obj.Pkg().Name() + "." + obj.Name() + "." + fname, iface, true
}

func isChanField(t types.Type) bool {
	_, ok := t.Underlying().(*types.Chan)
	return ok
}

// typeTag is the A16 discriminant: a rename-unstable but program-wide-consistent
// identity for one CONCRETE type, hashed so the core can compare two of them
// without carrying Go type strings (which contain '[' and would collide with
// slot_str's field-path syntax).
func typeTag(t types.Type) []byte {
	return hash.IIDFromParts("", "", "type:"+t.String(), "")
}

// assertsOf returns the type assertions applied to v, and whether they are v's
// ONLY uses. That "only" is what makes A16 recall-safe: the ledger reader also
// does `fmt.Errorf(..., event.EventSrc)` on the failure branch, and a cell read
// that is used raw anywhere must keep its unconstrained seed. x/tools SSA emits
// a separate FieldAddr+load per syntactic occurrence (no CSE), so the raw use is
// a different value and only the guarded load is narrowed.
func assertsOf(v ssa.Value) ([]*ssa.TypeAssert, bool) {
	refs := v.Referrers()
	if refs == nil || len(*refs) == 0 {
		return nil, false
	}
	out := make([]*ssa.TypeAssert, 0, len(*refs))
	for _, r := range *refs {
		ta, ok := r.(*ssa.TypeAssert)
		if !ok {
			return nil, false
		}
		out = append(out, ta)
	}
	return out, true
}

// concreteStored peels the interface boxing off a value stored into an
// interface-typed field, yielding the concrete type the write actually deposits
// in the cell. `false` ⇒ unknown (an interface-to-interface copy, a param, a
// call result already of interface type) ⇒ the write stays unconstrained.
func concreteStored(v ssa.Value) (types.Type, bool) {
	for i := 0; i < 8; i++ { // bounded: boxing chains here are 1-2 long
		switch it := v.(type) {
		case *ssa.MakeInterface:
			t := it.X.Type()
			if types.IsInterface(t) {
				return nil, false
			}
			return t, true
		case *ssa.ChangeType:
			v = it.X
		default:
			return nil, false
		}
	}
	return nil, false
}

// heapRead marks v as a READ of the cell: an IN_GLOBAL in-slot, so taint parked
// in the cell by some other function enters here.
//
// Heap in-slots are WHOLE-CELL only — they are not added to b.ordered, so
// emitAccessVariants never walks them. That is a deliberate cost/precision
// trade: with --heap-slots-scope=all there is a field load on nearly every
// line, and giving each one a path-variant walk is the O(n^2) shape that
// produced the 80.9 GB swap storm of 2026-07-19.
func (b *builder) heapRead(typ types.Type, field int, v ssa.Value) {
	sym, name, iface, ok := b.heapSym(typ, field)
	if !ok {
		return
	}
	// A16 (doc 30 §6.1). An interface-typed cell read ONLY through type
	// assertions is not really read here — the assertion is. Seed each assert
	// RESULT instead, tagged with the concrete type it demands, so the core can
	// drop the writers that cannot satisfy it. Leaving the plain `v -> assert`
	// edge in place matters: taint arriving at v from a tainted STRUCT (rather
	// than from the cell) must still reach the assertion.
	if b.opts.HeapIfaceNarrow && iface {
		if tas, only := assertsOf(v); only {
			for _, ta := range tas {
				id := b.addVertex(pb.VertexKind_IN_GLOBAL, 0, 0, ta.Type().String(), nil)
				vx := b.flow.Vertices[id]
				vx.Sym, vx.SymName = sym, name
				// An assertion to an INTERFACE is not a discriminant we can
				// check without method sets, so it narrows nothing (sound).
				if !types.IsInterface(ta.AssertedType) {
					vx.IfaceType = typeTag(ta.AssertedType)
				}
				b.srcVerts[ta] = append(b.srcVerts[ta], id)
			}
			return
		}
	}
	id := b.addVertex(pb.VertexKind_IN_GLOBAL, 0, 0, v.Type().String(), nil)
	vx := b.flow.Vertices[id]
	vx.Sym, vx.SymName = sym, name
	b.srcVerts[v] = append(b.srcVerts[v], id)
}

// heapWrite marks val as flowing INTO the cell: an OUT_FIELD out-slot carrying
// sym, which the core reads as Global(sym) rather than Field(index). span is
// the write site — the route hop the witness renders when it crosses the cell.
func (b *builder) heapWrite(typ types.Type, field int, val ssa.Value, span *pb.Span) {
	sym, name, iface, ok := b.heapSym(typ, field)
	if !ok {
		return
	}
	id := b.addVertex(pb.VertexKind_OUT_FIELD, 0, 0, val.Type().String(), span)
	vx := b.flow.Vertices[id]
	vx.Sym, vx.SymName = sym, name
	// A16: which concrete type THIS write deposits in the cell. The vertex's own
	// `type` is the interface (that is what is being stored), so the boxing has
	// to be peeled. Filtered per write site in the core's `propagate`, which is
	// where the existing per-write-site path filter already lives.
	if b.opts.HeapIfaceNarrow && iface {
		if ct, ok := concreteStored(val); ok {
			vx.IfaceType = typeTag(ct)
		}
	}
	b.sinkVerts[val] = append(b.sinkVerts[val], id)
}

// chanCellOf resolves the heap cell a channel VALUE was loaded from, walking
// the copies SSA inserts between `&p.incoming` and the send. Returns ok=false
// for a channel that is not a struct field (a local, a param) — those keep
// today's behaviour, which is to lose the send.
func (b *builder) chanCellOf(ch ssa.Value) (types.Type, int, bool) {
	for i := 0; i < 8; i++ { // bounded: SSA copy chains here are 1-2 long
		switch v := ch.(type) {
		case *ssa.UnOp:
			if v.Op != token.MUL {
				return nil, 0, false
			}
			ch = v.X
		case *ssa.FieldAddr:
			return v.X.Type(), v.Field, true
		case *ssa.Field:
			return v.X.Type(), v.Field, true
		case *ssa.ChangeType:
			ch = v.X
		default:
			return nil, 0, false
		}
	}
	return nil, 0, false
}

// heapHook emits the heap-cell reads/writes for one instruction. No-op unless
// --heap-slots; every emission is additive (new vertices only), so the OFF tree
// is bit-for-bit the pre-W1F one.
func (b *builder) heapHook(instr ssa.Instruction) {
	if !b.opts.HeapSlots {
		return
	}
	switch it := instr.(type) {
	case *ssa.UnOp:
		// `*(&x.f)` — the load. (`<-ch` is also a UnOp; its cell, if any, is
		// picked up because the CHANNEL value is itself such a load.)
		if it.Op == token.MUL {
			if fa, ok := it.X.(*ssa.FieldAddr); ok {
				b.heapRead(fa.X.Type(), fa.Field, it)
			}
		}
	case *ssa.Field:
		b.heapRead(it.X.Type(), it.Field, it)
	case *ssa.Store:
		if fa, ok := it.Addr.(*ssa.FieldAddr); ok {
			b.heapWrite(fa.X.Type(), fa.Field, it.Val, b.span(it))
		}
	case *ssa.Send:
		// `ch <- v` is not an ssa.Value, so the generic arm drops it outright.
		if typ, f, ok := b.chanCellOf(it.Chan); ok {
			b.heapWrite(typ, f, it.X, b.span(it))
		}
	case *ssa.Select:
		// 40 of ledger-svc's 52 field-sends are select cases (doc 28 §1c), so
		// this arm — not *ssa.Send — is the one that matters in practice.
		for _, st := range it.States {
			if st.Dir != types.SendOnly || st.Send == nil {
				continue
			}
			if typ, f, ok := b.chanCellOf(st.Chan); ok {
				b.heapWrite(typ, f, st.Send, b.span(it))
			}
		}
	}
}

// projField resolves the field identity for a projection/injection on typ
// (deref'd to a named struct). false ⇒ caller falls back to the whole-object
// edge — mandatory for embedded promotion weirdness, type params, and the
// null-wrapper collapse (doc 20 §2.2.4: the inner deref consumes no path).
func (b *builder) projField(typ types.Type, field int) (uint32, string, bool) {
	if !b.opts.FieldPaths {
		return 0, "", false
	}
	st, named := structOf(typ)
	if st == nil || field < 0 || field >= st.NumFields() {
		return 0, "", false
	}
	if named != nil && isNullWrapper(named) {
		return 0, "", false
	}
	fid, name := fieldID(st, field)
	return fid, name, true
}

// pathState is the BFS fact: the traversed value is tainted at this field
// path ([] = whole). Fixed-size for map keys; plen ≤ maxFieldPath.
type pathState struct {
	p    [maxFieldPath]uint32
	n    [maxFieldPath]string // names, reporting only (not part of identity)
	plen uint8
}

// step applies an edge op to the state; ok=false ⇒ the fact does not flow
// through this edge. srcDepth = |field_path| of the BFS's source vertex.
//
// The srcDepth rule keeps summaries precise: a whole-path fact crossing a
// projection must NOT continue as the base source — that flow's real
// dependency is the projected field, and the deeper path-variant source
// (emitAccessVariants) owns it. Otherwise every projected flow would also
// emit a spurious whole-object row, and prefix matching in the core would
// fire it for disjoint-field caller facts (the exact FP doc 20 §2 kills).
// At srcDepth == k there is no deeper variant — collapse and traverse.
func (s pathState) step(e edge, srcDepth int) (pathState, bool) {
	switch e.op {
	case opProj:
		if s.plen == 0 {
			return s, srcDepth >= maxFieldPath
		}
		if s.p[0] != e.field {
			return s, false // disjoint field: the precision doc 20 §2 buys
		}
		var out pathState
		copy(out.p[:], s.p[1:])
		copy(out.n[:], s.n[1:])
		out.plen = s.plen - 1
		return out, true
	case opInject:
		var out pathState
		out.p[0], out.n[0] = e.field, e.name
		// k-truncation point 1: the dropped tail collapses to "this subtree"
		keep := int(s.plen)
		if keep > maxFieldPath-1 {
			keep = maxFieldPath - 1
		}
		copy(out.p[1:], s.p[:keep])
		copy(out.n[1:], s.n[:keep])
		out.plen = uint8(keep) + 1
		return out, true
	default:
		return s, true
	}
}

// computeEdges: multi-source reachability from every source-vertex over the
// call-barriered value-flow graph; emit a LocalFlow edge on reaching a sink.
// Facts carry a field path; a sink reached at a non-empty path lands on a
// path-variant of the sink vertex, materialized deterministically below (BFS
// order must never pick a vertex id — bid hashes the marshaled flow).
func (b *builder) computeEdges() {
	type qitem struct {
		v     ssa.Value
		ps    pathState
		alias bool
	}
	type visitKey struct {
		v ssa.Value
		p [maxFieldPath]uint32
		l uint8
	}
	type pending struct {
		from, sink uint32
		ps         pathState
		alias      bool
	}
	var pend []pending
	for sv, srcIDs := range b.srcVerts {
		for _, srcID := range srcIDs {
			srcDepth := len(b.flow.Vertices[srcID].FieldPath)
			visited := map[visitKey]bool{}
			q := []qitem{{v: sv}}
			visited[visitKey{v: sv}] = true
			// a source value that is also a sink value (param directly returned)
			for len(q) > 0 {
				cur := q[0]
				q = q[1:]
				if sinks, ok := b.sinkVerts[cur.v]; ok {
					for _, sid := range sinks {
						pend = append(pend, pending{srcID, sid, cur.ps, cur.alias})
					}
				}
				for _, e := range b.succ[cur.v] {
					ns, ok := cur.ps.step(e, srcDepth)
					if !ok {
						continue
					}
					k := visitKey{e.to, ns.p, ns.plen}
					if !visited[k] {
						visited[k] = true
						q = append(q, qitem{e.to, ns, cur.alias || e.alias})
					}
				}
			}
		}
	}

	// materialize sink path-variants in sorted order, then emit edges (the
	// final edge sort in Build handles edge order; vertex ids must be
	// BFS-order-independent).
	type vkey struct {
		sink uint32
		p    [maxFieldPath]uint32
		l    uint8
	}
	sort.Slice(pend, func(i, j int) bool {
		a, c := pend[i], pend[j]
		if a.sink != c.sink {
			return a.sink < c.sink
		}
		if a.ps.plen != c.ps.plen {
			return a.ps.plen < c.ps.plen
		}
		if a.ps.p != c.ps.p {
			for k := 0; k < maxFieldPath; k++ {
				if a.ps.p[k] != c.ps.p[k] {
					return a.ps.p[k] < c.ps.p[k]
				}
			}
		}
		if a.from != c.from {
			return a.from < c.from
		}
		return !a.alias && c.alias
	})
	variant := map[vkey]uint32{}
	seen := map[[2]uint32]bool{} // dedupe (srcVert, toVert)
	for _, p := range pend {
		to := p.sink
		if p.ps.plen > 0 {
			k := vkey{p.sink, p.ps.p, p.ps.plen}
			id, ok := variant[k]
			if !ok {
				base := b.flow.Vertices[p.sink]
				id = b.addVertex(base.Kind, base.Index, base.CallsiteId, base.Type, nil)
				v := b.flow.Vertices[id]
				v.FieldPath = append([]uint32{}, p.ps.p[:p.ps.plen]...)
				v.FieldNames = append([]string{}, p.ps.n[:p.ps.plen]...)
				// a path-variant of a heap-cell out-slot is still that cell —
				// without this the variant degrades to Field(index) in the core
				// and the write silently addresses a different slot. nil for
				// every non-W1F vertex, so proto3 omits it and OFF is unchanged.
				v.Sym, v.SymName = base.Sym, base.SymName
				variant[k] = id
			}
			to = id
		}
		key := [2]uint32{p.from, to}
		if !seen[key] {
			seen[key] = true
			b.flow.Edges = append(b.flow.Edges, &pb.FlowEdge{From: p.from, To: to, ViaAlias: p.alias})
		}
	}
}

// emitAccessVariants walks plain/proj edges from every source-side base vertex
// (params, receiver, call results) and materializes one path-variant vertex
// per accessed path (k-truncation point 2): the sparse in-slot vocabulary of
// doc 20 §2.5 — a path exists only if the code projects it. Each variant is
// registered as a source vertex of the projected value(s), so its outgoing
// LocalFlow edges are computed by the ordinary BFS; the core seeds it when a
// caller fact matches its (slot, path).
func (b *builder) emitAccessVariants() {
	for _, ent := range b.ordered {
		type wkey struct {
			v ssa.Value
			p [maxFieldPath]uint32
			l uint8
		}
		type wstate struct {
			v  ssa.Value
			ps pathState
		}
		visited := map[wkey]bool{}
		q := []wstate{{v: ent.val}}
		visited[wkey{v: ent.val}] = true
		// accessed path -> projected values reached at it (path order fixed
		// by collecting into a slice keyed on first arrival, then sorting)
		type acc struct {
			ps   pathState
			vals []ssa.Value
		}
		byPath := map[[maxFieldPath + 1]uint32]*acc{}
		var order [][maxFieldPath + 1]uint32
		for len(q) > 0 {
			cur := q[0]
			q = q[1:]
			for _, e := range b.succ[cur.v] {
				var ns pathState
				switch e.op {
				case opProj:
					if int(cur.ps.plen) >= maxFieldPath {
						continue // deeper accesses collapse to the k-prefix variant
					}
					ns = cur.ps
					ns.p[cur.ps.plen], ns.n[cur.ps.plen] = e.field, e.name
					ns.plen++
				case opInject:
					continue // vocabulary = read paths only
				default:
					ns = cur.ps // plain copy: same accessed path
				}
				k := wkey{e.to, ns.p, ns.plen}
				if visited[k] {
					continue
				}
				visited[k] = true
				q = append(q, wstate{e.to, ns})
				if ns.plen > 0 {
					var pk [maxFieldPath + 1]uint32
					pk[0] = uint32(ns.plen)
					copy(pk[1:], ns.p[:])
					a, ok := byPath[pk]
					if !ok {
						a = &acc{ps: ns}
						byPath[pk] = a
						order = append(order, pk)
					}
					a.vals = append(a.vals, e.to)
				}
			}
		}
		sort.Slice(order, func(i, j int) bool {
			for k := 0; k <= maxFieldPath; k++ {
				if order[i][k] != order[j][k] {
					return order[i][k] < order[j][k]
				}
			}
			return false
		})
		base := b.flow.Vertices[ent.vid]
		for _, pk := range order {
			a := byPath[pk]
			id := b.addVertex(base.Kind, base.Index, base.CallsiteId, base.Type, nil)
			v := b.flow.Vertices[id]
			v.FieldPath = append([]uint32{}, a.ps.p[:a.ps.plen]...)
			v.FieldNames = append([]string{}, a.ps.n[:a.ps.plen]...)
			v.Sym, v.SymName = base.Sym, base.SymName
			for _, val := range a.vals {
				b.srcVerts[val] = append(b.srcVerts[val], id)
			}
		}
	}
}

func (b *builder) wireExtracts() {
	// map Extract(call, i) -> the i-th result port of that call
	for _, blk := range b.fn.Blocks {
		for _, instr := range blk.Instrs {
			ex, ok := instr.(*ssa.Extract)
			if !ok {
				continue
			}
			if v, ok := b.resultPort[[2]interface{}{ex.Tuple, uint32(ex.Index)}]; ok {
				b.srcVerts[ex] = append(b.srcVerts[ex], v)
				b.ordered = append(b.ordered, srcEntry{ex, v})
			}
		}
	}
}

func (b *builder) span(instr ssa.Instruction) *pb.Span {
	if instr == nil {
		return nil
	}
	return b.posSpan(instr.Pos())
}

func (b *builder) posSpan(pos token.Pos) *pb.Span {
	if !pos.IsValid() || b.fn.Prog == nil {
		return nil
	}
	p := b.fn.Prog.Fset.Position(pos)
	return &pb.Span{File: p.Filename, Line: int32(p.Line), Col: int32(p.Column)}
}

// errorIface is the `error` interface itself — the target of the Implements test
// so concrete error types (*pgconn.PgError, *status.Status) count as errors too.
var errorIface = types.Universe.Lookup("error").Type().Underlying().(*types.Interface)

// errorResultMask sets bit j when result j of this callsite is `error`-typed
// (B1, doc 31 §4). Result ports carry no `type` of their own (:585 emits ""), so
// this mask is the only way the core can tell the error apart from the value —
// and it must be exact, not "the last result": doc 31 §3a measured the
// convention proxy at 21 of 278 keys because 1-result producers dominate the
// keys while multi-result ones dominate the sites.
//
// Bit 63 is never set: the mask is a u64 and a Go call cannot return 64 values
// in practice, but a malformed signature must not wrap around into bit 0.
func errorResultMask(cc *ssa.CallCommon, resultc uint32) uint64 {
	sig := cc.Signature()
	if sig == nil {
		return 0
	}
	res := sig.Results()
	var mask uint64
	for j := 0; j < res.Len() && j < 63 && uint32(j) < resultc; j++ {
		if types.Implements(res.At(j).Type(), errorIface) {
			mask |= 1 << uint(j)
		}
	}
	return mask
}

func numResults(cc *ssa.CallCommon) int {
	sig := cc.Signature()
	if sig == nil {
		return 0
	}
	return sig.Results().Len()
}

// remoteContract recognizes a call on a generated gRPC <Svc>Client interface
// and returns the contract name "<pkg>.<Service>/<Method>", where <pkg> is the
// Go package name of the generated code, not the .proto package. Both the client
// repo and the server repo derive the same string, so ContractIID links them.
func remoteContract(cc *ssa.CallCommon, pbp pkgclass.PbPaths) (string, bool) {
	if cc.Method == nil {
		return "", false
	}
	t := cc.Value.Type()
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return "", false
	}
	tn := named.Obj()
	name := tn.Name()
	if name == "Client" || !strings.HasSuffix(name, "Client") {
		return "", false
	}
	pkg := tn.Pkg()
	if pkg == nil || !pbp.Match(pkg.Path()) {
		return "", false
	}
	svc := strings.TrimSuffix(name, "Client")
	if strings.Contains(svc, "_") {
		// Svc_MethodClient is a generated stream object, not a service client;
		// its data ports are handled by streamPort.
		return "", false
	}
	return pkg.Name() + "." + svc + "/" + cc.Method.Name(), true
}

// streamPort recognizes invoke calls on generated stream interfaces
// "<Svc>_<Method>Server" / "<Svc>_<Method>Client" in a pb package and returns
// the contract full name plus the data-port direction. Non-data methods
// (Header, Trailer, CloseSend, Context, ...) are not ports. The first "_" is
// the codegen separator: protoc-gen-go camel-cases proto idents, so generated
// Go service/method names themselves contain no underscore.
func streamPort(cc *ssa.CallCommon, pbp pkgclass.PbPaths) (full string, op pb.CallSite_StreamOp, clientSide bool, ok bool) {
	if cc.Method == nil {
		return "", 0, false, false
	}
	named, isNamed := cc.Value.Type().(*types.Named)
	if !isNamed {
		return "", 0, false, false
	}
	tn := named.Obj()
	name := tn.Name()
	var suffix string
	switch {
	case strings.HasSuffix(name, "Server"):
		suffix, clientSide = "Server", false
	case strings.HasSuffix(name, "Client"):
		suffix, clientSide = "Client", true
	default:
		return "", 0, false, false
	}
	svc, method, found := strings.Cut(strings.TrimSuffix(name, suffix), "_")
	if !found || svc == "" || method == "" {
		return "", 0, false, false
	}
	if tn.Pkg() == nil || !pbp.Match(tn.Pkg().Path()) {
		return "", 0, false, false
	}
	switch cc.Method.Name() {
	case "Send", "SendMsg", "SendAndClose":
		op = pb.CallSite_STREAM_OP_SEND
	case "Recv", "RecvMsg", "CloseAndRecv":
		op = pb.CallSite_STREAM_OP_RECV
	default:
		return "", 0, false, false
	}
	return tn.Pkg().Name() + "." + svc + "/" + method, op, clientSide, true
}

// ---- field-path helpers (doc 20 §2) ----

// structOf derefs pointers and unwraps the named type to its struct
// underlying. nil struct ⇒ not a resolvable struct projection.
func structOf(typ types.Type) (*types.Struct, *types.Named) {
	t := typ
	if ptr, ok := t.Underlying().(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, _ := t.(*types.Named)
	st, _ := t.Underlying().(*types.Struct)
	return st, named
}

// isNullWrapper: nullable scalar wrappers are path-transparent (doc 20
// §2.2.4) — projecting .String out of sql.NullString consumes no path
// element. Table changes are covered by the pc-fe binary hash in cache keys.
func isNullWrapper(named *types.Named) bool {
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	path, name := obj.Pkg().Path(), obj.Name()
	if path == "database/sql" && strings.HasPrefix(name, "Null") {
		return true
	}
	return isNullWrapperPkg(path)
}

// nullWrapperModules is the explicit allowlist (assessment §3.2). The old rule
// was a bare `/null` or `/zero` SUFFIX, which collapses a field path through
// any third-party package that happens to end in one of those words — a silent
// precision loss in someone else's repo. Module prefixes only: subpackages
// (`guregu/null/zero`) and major-version suffixes (`/v4`, `.v4`) match.
var nullWrapperModules = []string{
	"github.com/guregu/null",
	"gopkg.in/guregu/null",
	"github.com/volatiletech/null",
	"github.com/volatiletech/sqlboiler/types/null",
	"google.golang.org/protobuf/types/known/wrapperspb",
}

func isNullWrapperPkg(path string) bool {
	for _, m := range nullWrapperModules {
		if path == m || strings.HasPrefix(path, m+"/") || strings.HasPrefix(path, m+".") {
			return true
		}
	}
	return false
}

// fieldID: stable field identity — the protobuf field number from the struct
// tag when the type is proto-generated (rename-stable, aligns cross-repo),
// else the go/types struct index (doc 20 §2.1).
func fieldID(st *types.Struct, i int) (uint32, string) {
	name := st.Field(i).Name()
	if tag := reflect.StructTag(st.Tag(i)).Get("protobuf"); tag != "" {
		parts := strings.Split(tag, ",")
		if len(parts) > 1 {
			if n, err := strconv.Atoi(parts[1]); err == nil && n >= 0 {
				return uint32(n), name
			}
		}
	}
	return uint32(i), name
}

// isCopyBuiltin reports whether cc is the `copy(dst, src)` builtin.
func isCopyBuiltin(cc *ssa.CallCommon) bool {
	if cc.IsInvoke() {
		return false
	}
	bi, ok := cc.Value.(*ssa.Builtin)
	return ok && bi.Name() == "copy" && len(cc.Args) == 2
}

// IsCanonicalizedCall reports whether Build with field paths on emits NO
// CallSite for instr (trivial pb getter → projection edge).
func IsCanonicalizedCall(instr ssa.CallInstruction, pbp pkgclass.PbPaths) bool {
	if instr.Value() == nil {
		return false
	}
	_, _, ok := trivialPbGetter(instr.Common(), pbp)
	return ok
}

// CanonicalizedCallFilter returns the predicate matching the CallInstructions
// that Build under o emits NO CallSite for. The cgstore snapshot layer
// re-enumerates CallInstructions to recover CallSite.Id ordinals, so it must
// apply exactly this filter or every id shifts by the number of canonicalized
// calls before it. nil means "nothing is canonicalized" — the pre-doc-20
// behaviour, and the value emit passes when both features are off.
func CanonicalizedCallFilter(o Opts) func(ssa.CallInstruction) bool {
	if !o.FieldPaths && !o.HeapSlots {
		return nil
	}
	return func(instr ssa.CallInstruction) bool {
		if o.FieldPaths && IsCanonicalizedCall(instr, o.PbPaths) {
			return true
		}
		return o.HeapSlots && isCopyBuiltin(instr.Common())
	}
}

// trivialPbGetter recognizes `x.GetFoo()` on a pb-package struct whose SSA
// body is verifiably `return x.Foo` (modulo the generated nil-check). The SSA
// body check is the misclassification guard: a hand-written GetX with logic
// keeps its barrier, so its sinks/summaries are never skipped.
func trivialPbGetter(cc *ssa.CallCommon, pbp pkgclass.PbPaths) (uint32, string, bool) {
	if cc.IsInvoke() {
		return 0, "", false
	}
	sc := cc.StaticCallee()
	if sc == nil || sc.Signature.Recv() == nil || len(cc.Args) != 1 {
		return 0, "", false
	}
	if sc.Signature.Params().Len() != 0 || sc.Signature.Results().Len() != 1 {
		return 0, "", false
	}
	name := sc.Name()
	if !strings.HasPrefix(name, "Get") || len(name) <= 3 {
		return 0, "", false
	}
	st, named := structOf(sc.Signature.Recv().Type())
	if st == nil || named == nil || isNullWrapper(named) {
		return 0, "", false
	}
	pkg := named.Obj().Pkg()
	if pkg == nil || !pbp.Match(pkg.Path()) {
		return 0, "", false
	}
	fieldIdx := -1
	for i := 0; i < st.NumFields(); i++ {
		if st.Field(i).Name() == name[3:] {
			fieldIdx = i
			break
		}
	}
	if fieldIdx < 0 || !getterReturnsField(sc, fieldIdx) {
		return 0, "", false
	}
	fid, fname := fieldID(st, fieldIdx)
	return fid, fname, true
}

// getterReturnsField verifies every return in sc's body yields either a
// constant (the generated nil-check's zero branch) or recv.<fieldIdx>, and no
// calls occur. Bodiless (unbuilt/external) getters fail — barrier stays.
func getterReturnsField(sc *ssa.Function, fieldIdx int) bool {
	if len(sc.Blocks) == 0 || len(sc.Params) != 1 {
		return false
	}
	recv := sc.Params[0]
	fieldOfRecv := func(v ssa.Value) bool {
		switch r := v.(type) {
		case *ssa.Field:
			return r.X == recv && r.Field == fieldIdx
		case *ssa.UnOp:
			fa, ok := r.X.(*ssa.FieldAddr)
			return ok && fa.X == recv && fa.Field == fieldIdx
		}
		return false
	}
	sawField := false
	for _, blk := range sc.Blocks {
		for _, ins := range blk.Instrs {
			switch it := ins.(type) {
			case ssa.CallInstruction:
				return false
			case *ssa.Return:
				if len(it.Results) != 1 {
					return false
				}
				if _, isConst := it.Results[0].(*ssa.Const); isConst {
					continue
				}
				if !fieldOfRecv(it.Results[0]) {
					return false
				}
				sawField = true
			}
		}
	}
	return sawField
}
