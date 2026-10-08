package flow

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/panoptiorg/panoptife-go/internal/callgraph"
	pb "github.com/panoptiorg/panoptife-go/internal/cgfpb"
	"github.com/panoptiorg/panoptife-go/internal/loader"
)

type csInfo struct {
	kind       pb.CallSite_Kind
	remote     string
	op         pb.CallSite_StreamOp
	clientSide bool
	fqn        string
}

func buildFor(t *testing.T, ld *loader.Loaded, fqnPart string) []csInfo {
	t.Helper()
	var fn *ssa.Function
	for f := range ssautil.AllFunctions(ld.Prog) {
		if strings.Contains(f.String(), fqnPart) && f.Blocks != nil {
			fn = f
			break
		}
	}
	if fn == nil {
		t.Fatalf("function %q not found", fqnPart)
	}
	cg, _ := loader.BuildCallGraph(ld.Prog, "vta")
	res := Build(fn, callgraph.NewResolver(cg, callgraph.DefaultFanoutCap, nil), Opts{FieldPaths: true, ClosureFlow: true})
	var out []csInfo
	for i, cs := range res.Flow.Callsites {
		out = append(out, csInfo{
			kind:       cs.Kind,
			remote:     res.Callees[i].RemoteFullName,
			op:         cs.StreamOp,
			clientSide: cs.StreamClientSide,
			fqn:        cs.CalleeFqn,
		})
	}
	return out
}

func find(t *testing.T, css []csInfo, pred func(csInfo) bool, desc string) csInfo {
	t.Helper()
	for _, c := range css {
		if pred(c) {
			return c
		}
	}
	t.Fatalf("no callsite matching: %s (have %+v)", desc, css)
	return csInfo{}
}

// buildFnFor returns the *ssa.Function plus its Result (buildFor throws the
// Result away and keeps only callsite shape). Matching is EXACT: AllFunctions
// iterates a map, and a substring match on "…Captures" also hits
// "…Captures$1", so a prefix match picks a different function run to run.
func buildFnFor(t *testing.T, ld *loader.Loaded, fqn string, closureFlow bool) (*ssa.Function, *Result) {
	t.Helper()
	var fn *ssa.Function
	for f := range ssautil.AllFunctions(ld.Prog) {
		if f.String() == fqn && f.Blocks != nil {
			fn = f
			break
		}
	}
	if fn == nil {
		t.Fatalf("function %q not found", fqn)
	}
	return fn, Build(fn, nil, Opts{FieldPaths: true, ClosureFlow: closureFlow})
}

// The W1d contract, all four clauses at once (doc 24 §5 W1d):
//   - the closure's free var is an IN_PARAM at the index just past the declared
//     params, so the core's arg-port -> Param(k) remap needs no change;
//   - the binding site is a CallSite appended AFTER every real one;
//   - its arg port carries the SAME index, and its callee is the closure;
//   - NCallInstrs counts only the real ones — the number cgstore validates.
func TestClosureBindingSite(t *testing.T) {
	ld, err := loader.Load("testdata/closures", "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	_, res := buildFnFor(t, ld, "example.com/closures.Captures", true)
	if res.NCallInstrs != len(res.Flow.Callsites)-1 {
		t.Fatalf("want exactly one synthetic site past NCallInstrs, got NCallInstrs=%d of %d",
			res.NCallInstrs, len(res.Flow.Callsites))
	}
	bind := res.Flow.Callsites[res.NCallInstrs]
	if !strings.Contains(bind.CalleeFqn, "example.com/closures.Captures$1") {
		t.Errorf("binding site callee = %q, want the closure Captures$1", bind.CalleeFqn)
	}
	if bind.Resultc != 0 || bind.Arg0IsReceiver {
		t.Errorf("binding site: resultc=%d arg0_is_receiver=%v, want 0/false", bind.Resultc, bind.Arg0IsReceiver)
	}
	if res.Callees[res.NCallInstrs].Static == nil {
		t.Errorf("binding site callee must resolve statically to the closure")
	}
	// the closure declares one param (tx) => its single free var is param_1,
	// and the binding arg port must address that same index.
	var argIdx []uint32
	for _, v := range res.Flow.Vertices {
		if v.Kind == pb.VertexKind_CALL_ARG_PORT && v.CallsiteId == bind.Id {
			argIdx = append(argIdx, v.Index)
		}
	}
	if len(argIdx) != 1 || argIdx[0] != 1 {
		t.Errorf("binding arg ports = %v, want exactly [1] (past the closure's one declared param)", argIdx)
	}

	closFn, closRes := buildFnFor(t, ld, "example.com/closures.Captures$1", true)
	if n := len(closFn.FreeVars); n != 1 {
		t.Fatalf("Captures$1 free vars = %d, want 1", n)
	}
	var inParams []uint32
	for _, v := range closRes.Flow.Vertices {
		if v.Kind == pb.VertexKind_IN_PARAM && len(v.FieldPath) == 0 {
			inParams = append(inParams, v.Index)
		}
	}
	if len(inParams) != 2 || inParams[0] != 0 || inParams[1] != 1 {
		t.Errorf("closure in-params = %v, want [0 1] (tx, then the capture)", inParams)
	}
}

// A `$bound` wrapper's free var is the captured receiver, and it has no
// receiver of its own — so the capture lands at index len(Params), not shifted.
func TestClosureBindingBoundMethod(t *testing.T) {
	ld, err := loader.Load("testdata/closures", "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	_, res := buildFnFor(t, ld, "example.com/closures.BoundMethod", true)
	if res.NCallInstrs != 0 || len(res.Flow.Callsites) != 1 {
		t.Fatalf("BoundMethod: callsites=%d NCallInstrs=%d, want 1/0",
			len(res.Flow.Callsites), res.NCallInstrs)
	}
	bind := res.Flow.Callsites[0]
	if !strings.Contains(bind.CalleeFqn, "$bound") {
		t.Errorf("callee = %q, want the $bound wrapper", bind.CalleeFqn)
	}
	for _, v := range res.Flow.Vertices {
		if v.Kind == pb.VertexKind_CALL_ARG_PORT && v.Index != 1 {
			t.Errorf("$bound capture arg port index = %d, want 1 (one declared param)", v.Index)
		}
	}
}

// A capture-free closure emits no binding site, and --closure-flow=false emits
// none at all — the escape hatch the corpus A/B depends on.
func TestClosureBindingOffAndEmpty(t *testing.T) {
	ld, err := loader.Load("testdata/closures", "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, res := buildFnFor(t, ld, "example.com/closures.NoCapture", true); len(res.Flow.Callsites) != 0 {
		t.Errorf("NoCapture: %d callsites, want 0 (MakeClosure with no bindings)", len(res.Flow.Callsites))
	}
	_, off := buildFnFor(t, ld, "example.com/closures.Captures", false)
	if off.NCallInstrs != len(off.Flow.Callsites) {
		t.Errorf("closure-flow off: %d synthetic sites leaked", len(off.Flow.Callsites)-off.NCallInstrs)
	}
	_, closOff := buildFnFor(t, ld, "example.com/closures.Captures$1", false)
	for _, v := range closOff.Flow.Vertices {
		if v.Kind == pb.VertexKind_IN_PARAM && v.Index > 0 {
			t.Errorf("closure-flow off: free-var in-slot param_%d leaked", v.Index)
		}
	}
}

// The cgstore contract, stated where it can be broken: NCallInstrs must equal
// what callgraph.callInstructions re-enumerates on reload (fn.Blocks/Instrs
// order, canonicalized getters filtered out). If a future change emits a
// synthetic site in the middle of the real ones, or forgets to exclude it from
// the count, NewSnapshotResolver rejects every caller and dispatch reuse dies
// silently — nothing else in the tree notices.
func TestNCallInstrsMatchesCallInstructionEnumeration(t *testing.T) {
	ld, err := loader.Load("testdata/closures", "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	checkNCallInstrs(t, ld, "example.com/closures", nil)
}

// Coverage wave 1 adds two more synthetic site kinds (`read:` and `http:`);
// they must land in the tail too, after the closure bindings.
func TestNCallInstrsWithCoverageSites(t *testing.T) {
	ld, err := loader.Load("../../fixtures/httpclient", "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	reads, calls := checkNCallInstrs(t, ld, "example.com/httpclient", []Opts{
		{FieldPaths: true, ClosureFlow: true, SurfaceReads: true, HTTPCalls: true, TopicCells: true},
		{FieldPaths: true, ClosureFlow: true, SurfaceReads: true, HTTPCalls: true, HeapSlots: true, ByRefOut: true},
	})
	if reads == 0 || calls == 0 {
		t.Fatalf("fixture drift: %d read: and %d http: sites checked", reads, calls)
	}
}

// checkNCallInstrs asserts the cgstore invariant for every function of pkg
// under each Opts (nil: the historical variants) and counts the coverage
// synthetic sites it saw in the tail.
func checkNCallInstrs(t *testing.T, ld *loader.Loaded, pkg string, extra []Opts) (reads, calls int) {
	t.Helper()
	checked := 0
	// W1F adds a SECOND canonicalized call (the `copy` builtin), so the invariant
	// is now per-Opts and the enumeration must use the same filter emit hands to
	// NewSnapshotResolver — not the bare IsCanonicalizedCall.
	variants := []Opts{
		{FieldPaths: true, ClosureFlow: true},
		{FieldPaths: true, ClosureFlow: true, HeapSlots: true},
		{FieldPaths: true, ClosureFlow: true, HeapSlots: true, HeapAllFields: true},
	}
	if extra != nil {
		variants = extra
	}
	for fn := range ssautil.AllFunctions(ld.Prog) {
		if fn.Blocks == nil || fn.Pkg == nil || fn.Pkg.Pkg.Path() != pkg {
			continue
		}
		for _, o := range variants {
			skip := CanonicalizedCallFilter(o)
			want := 0
			for _, blk := range fn.Blocks {
				for _, instr := range blk.Instrs {
					if call, ok := instr.(ssa.CallInstruction); ok && (skip == nil || !skip(call)) {
						want++
					}
				}
			}
			res := Build(fn, nil, o)
			if res.NCallInstrs != want {
				t.Errorf("%s %+v: NCallInstrs=%d, CallInstructions=%d", fn, o, res.NCallInstrs, want)
			}
			if res.NCallInstrs > len(res.Flow.Callsites) {
				t.Errorf("%s %+v: NCallInstrs=%d exceeds %d callsites", fn, o, res.NCallInstrs, len(res.Flow.Callsites))
			}
			// the real sites must be the PREFIX: every synthetic one is a binding
			// site, which is the only kind with no result ports and a closure callee.
			for i, cs := range res.Flow.Callsites[:res.NCallInstrs] {
				if cs.Id != uint32(i) {
					t.Errorf("%s: callsite %d has id %d — ids must stay dense and ordered", fn, i, cs.Id)
				}
				if cs.HttpCall != nil || strings.HasPrefix(cs.CalleeFqn, "read:") {
					t.Errorf("%s: synthetic site %q inside the real prefix", fn, cs.CalleeFqn)
				}
			}
			for _, cs := range res.Flow.Callsites[res.NCallInstrs:] {
				switch {
				case cs.HttpCall != nil:
					calls++
				case strings.HasPrefix(cs.CalleeFqn, "read:"):
					reads++
				}
			}
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no functions checked — fixture drift")
	}
	return reads, calls
}

func TestStreamPortsClientSide(t *testing.T) {
	ld, err := loader.Load("../contracts/testdata/streamsvc", "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	css := buildFor(t, ld, "client.Run")

	open := find(t, css, func(c csInfo) bool {
		return c.remote == "pb.Feed/Upload" && c.op == pb.CallSite_STREAM_OP_NONE
	}, "open Upload call")
	if open.kind != pb.CallSite_INVOKES_REMOTE {
		t.Errorf("open Upload: kind = %v, want INVOKES_REMOTE", open.kind)
	}

	send := find(t, css, func(c csInfo) bool {
		return c.op == pb.CallSite_STREAM_OP_SEND
	}, "stream Send")
	if send.kind != pb.CallSite_INVOKES_REMOTE || send.remote != "pb.Feed/Upload" || !send.clientSide {
		t.Errorf("Send: %+v, want INVOKES_REMOTE pb.Feed/Upload client-side", send)
	}

	// CloseAndRecv on the upload stream + Recv on the download stream.
	find(t, css, func(c csInfo) bool {
		return c.op == pb.CallSite_STREAM_OP_RECV && c.remote == "pb.Feed/Upload" && c.clientSide
	}, "Upload CloseAndRecv")
	find(t, css, func(c csInfo) bool {
		return c.op == pb.CallSite_STREAM_OP_RECV && c.remote == "pb.Feed/Download" && c.clientSide
	}, "Download Recv")

	// remoteContract hardening: CloseSend on Feed_DownloadClient must NOT be remote.
	closeSend := find(t, css, func(c csInfo) bool {
		return strings.HasSuffix(c.fqn, "CloseSend")
	}, "CloseSend")
	if closeSend.kind == pb.CallSite_INVOKES_REMOTE {
		t.Errorf("CloseSend: got INVOKES_REMOTE (remote=%q), want VIRTUAL", closeSend.remote)
	}
}

func TestStreamPortsServerSide(t *testing.T) {
	ld, err := loader.Load("../contracts/testdata/streamsvc", "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	up := buildFor(t, ld, "FeedImpl).Upload")
	recv := find(t, up, func(c csInfo) bool { return c.op == pb.CallSite_STREAM_OP_RECV }, "server Recv")
	if recv.clientSide || recv.remote != "pb.Feed/Upload" || recv.kind != pb.CallSite_INVOKES_REMOTE {
		t.Errorf("server Recv: %+v", recv)
	}
	sac := find(t, up, func(c csInfo) bool { return c.op == pb.CallSite_STREAM_OP_SEND }, "SendAndClose")
	if sac.clientSide || sac.remote != "pb.Feed/Upload" {
		t.Errorf("SendAndClose: %+v", sac)
	}

	dl := buildFor(t, ld, "FeedImpl).Download")
	send := find(t, dl, func(c csInfo) bool { return c.op == pb.CallSite_STREAM_OP_SEND }, "server Send")
	if send.clientSide || send.remote != "pb.Feed/Download" {
		t.Errorf("server Send: %+v", send)
	}
}

// fakeCensusDispatcher exercises both dynamic-dispatch outcomes the "opaque:"
// stderr census (internal/emit) needs distinguished: a capped-fanout site
// (interface invoke) vs. a genuinely unresolved dynamic site (func value,
// zero targets, not capped). Real capping/emptiness comes from
// callgraph.Resolver; this fakes both outcomes directly so the counters in
// Result are tested independent of any particular call graph shape.
type fakeCensusDispatcher struct{}

func (fakeCensusDispatcher) TargetsAt(site ssa.CallInstruction) ([]*ssa.Function, float32, bool) {
	if site.Common().IsInvoke() {
		return nil, 1.0, true // capped
	}
	return nil, 1.0, false // unresolved: zero targets, not capped
}

// TestCensusCountsCappedAndUnresolvedSites is the unit test for
// Result.CappedSites / Result.UnresolvedDynamicSites, which back pc-fe's
// "opaque:" stderr census line (emit.go). CallBoth has exactly one
// interface-invoke call site (r.Run()) and one dynamic func-value call site
// (f()), each routed through TargetsAt exactly once.
func TestCensusCountsCappedAndUnresolvedSites(t *testing.T) {
	ld, err := loader.Load("testdata/dispatchcensus", "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	fn, res := buildFnDispatched(t, ld, "example.com/dispatchcensus.CallBoth", fakeCensusDispatcher{})
	if fn == nil {
		t.Fatal("CallBoth not found")
	}
	if len(res.Flow.Callsites) != 2 {
		t.Fatalf("want 2 callsites (invoke + func value), got %d", len(res.Flow.Callsites))
	}
	if res.CappedSites != 1 {
		t.Errorf("CappedSites = %d, want 1 (the r.Run() invoke site)", res.CappedSites)
	}
	if res.UnresolvedDynamicSites != 1 {
		t.Errorf("UnresolvedDynamicSites = %d, want 1 (the f() func-value site)", res.UnresolvedDynamicSites)
	}
	opaqueCount := 0
	for _, cs := range res.Flow.Callsites {
		if cs.Opaque {
			opaqueCount++
		}
	}
	if opaqueCount != 2 {
		t.Errorf("both sites must report CallSite.Opaque, got %d/2", opaqueCount)
	}
}

// buildFnDispatched is buildFnFor but with an explicit Dispatcher instead of
// nil, needed to drive both dynamic-dispatch branches in handleCall.
func buildFnDispatched(t *testing.T, ld *loader.Loaded, fqn string, res Dispatcher) (*ssa.Function, *Result) {
	t.Helper()
	var fn *ssa.Function
	for f := range ssautil.AllFunctions(ld.Prog) {
		if f.String() == fqn && f.Blocks != nil {
			fn = f
			break
		}
	}
	if fn == nil {
		t.Fatalf("function %q not found", fqn)
	}
	return fn, Build(fn, res, Opts{})
}

// reachesSink: does LocalFlow carry param `param` (any path variant) to an arg
// port of the call to `sink`? computeEdges emits source->sink reachability
// edges directly, so one edge is the whole answer.
func reachesSink(res *Result, param uint32) bool {
	sinkCS := map[uint32]bool{}
	for _, cs := range res.Flow.Callsites {
		if strings.HasSuffix(cs.CalleeFqn, "containers.sink") {
			sinkCS[cs.Id] = true
		}
	}
	kind := map[uint32]*pb.FlowVertex{}
	for _, v := range res.Flow.Vertices {
		kind[v.Id] = v
	}
	for _, e := range res.Flow.Edges {
		from, to := kind[e.From], kind[e.To]
		if from.Kind == pb.VertexKind_IN_PARAM && from.Index == param &&
			to.Kind == pb.VertexKind_CALL_ARG_PORT && sinkCS[to.CallsiteId] {
			return true
		}
	}
	return false
}

// --container-writes: `m[k] = v`, `ch <- v` and a select send case put v INTO
// the container, so a later read in the same function sees it. None of the
// three instructions is an ssa.Value, so without the option the value reaches
// nothing — and the option off must reproduce exactly that (escape hatch).
func TestContainerWrites(t *testing.T) {
	ld, err := loader.Load("testdata/containers", "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	build := func(fqn string, o Opts) *Result {
		t.Helper()
		for f := range ssautil.AllFunctions(ld.Prog) {
			if f.String() == fqn && f.Blocks != nil {
				return Build(f, nil, o)
			}
		}
		t.Fatalf("function %q not found", fqn)
		return nil
	}
	on := Opts{FieldPaths: true, ClosureFlow: true, ContainerWrites: true}
	off := Opts{FieldPaths: true, ClosureFlow: true}

	for _, name := range []string{"MapWrite", "MapKey", "MapInField", "ChanSend", "SelectSend"} {
		fqn := "example.com/containers." + name
		if !reachesSink(build(fqn, on), 0) {
			t.Errorf("%s: q must reach sink with ContainerWrites on", name)
		}
		if reachesSink(build(fqn, off), 0) {
			t.Errorf("%s: q reached sink with ContainerWrites off — the escape hatch is not the old emission", name)
		}
	}
	// precision: a write into one container never taints a different one
	for _, name := range []string{"OtherMap", "OtherChan"} {
		if reachesSink(build("example.com/containers."+name, on), 0) {
			t.Errorf("%s: a write into one container tainted a different one", name)
		}
	}
}

// A send on a channel PARAM is a write the caller sees: under --byref-out the
// channel param gets an out-slot (builder.isByRefType, under ContainerWrites),
// and the sent value reaches it.
func TestContainerWritesChanParamByRef(t *testing.T) {
	ld, err := loader.Load("testdata/containers", "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var res *Result
	for f := range ssautil.AllFunctions(ld.Prog) {
		if f.String() == "example.com/containers.Produce" && f.Blocks != nil {
			res = Build(f, nil, Opts{FieldPaths: true, ContainerWrites: true, ByRefOut: true})
		}
	}
	if res == nil {
		t.Fatal("Produce not found")
	}
	kind := map[uint32]*pb.FlowVertex{}
	for _, v := range res.Flow.Vertices {
		kind[v.Id] = v
	}
	for _, e := range res.Flow.Edges {
		from, to := kind[e.From], kind[e.To]
		if from.Kind == pb.VertexKind_IN_PARAM && from.Index == 1 &&
			to.Kind == pb.VertexKind_OUT_PARAM_BYREF && to.Index == 0 {
			return
		}
	}
	t.Error("Produce: q (param 1) must reach the channel param's by-ref out-slot (param 0)")
}

// --library-writeback: at a call into library code, a by-ref argument's port
// flows back into the caller's value, so a core-side propagator that writes the
// port reaches the value's later uses. Returns whether an edge runs from the
// arg port `port` of the call to `callee` to an arg port of `sink`.
func writesBackToSink(res *Result, callee string, port uint32) bool {
	vx := map[uint32]*pb.FlowVertex{}
	for _, v := range res.Flow.Vertices {
		vx[v.Id] = v
	}
	fqn := func(v *pb.FlowVertex) string { return res.Flow.Callsites[v.CallsiteId].CalleeFqn }
	for _, e := range res.Flow.Edges {
		from, to := vx[e.From], vx[e.To]
		if from.Kind == pb.VertexKind_CALL_ARG_PORT && from.Index == port && strings.HasSuffix(fqn(from), callee) &&
			to.Kind == pb.VertexKind_CALL_ARG_PORT && strings.HasSuffix(fqn(to), "libwrites.sink") {
			return true
		}
	}
	return false
}

func TestLibraryWriteback(t *testing.T) {
	ld, err := loader.Load("testdata/libwrites", "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	build := func(fqn string, on bool) *Result {
		t.Helper()
		for f := range ssautil.AllFunctions(ld.Prog) {
			if f.String() == fqn && f.Blocks != nil {
				return Build(f, nil, Opts{FieldPaths: true, ClosureFlow: true, ContainerWrites: true, LibraryWriteback: on})
			}
		}
		t.Fatalf("function %q not found", fqn)
		return nil
	}
	// sb.WriteString(q): the receiver port (0) writes back into sb, whose
	// String() result is what reaches sink — through the default leaf, in the
	// core; here the edge must run port 0 -> String's receiver port, and on to
	// nothing else, so check it lands on the String call.
	b := build("example.com/libwrites.Builder", true)
	found := false
	vx := map[uint32]*pb.FlowVertex{}
	for _, v := range b.Flow.Vertices {
		vx[v.Id] = v
	}
	for _, e := range b.Flow.Edges {
		from, to := vx[e.From], vx[e.To]
		if from.Kind == pb.VertexKind_CALL_ARG_PORT && from.Index == 0 &&
			strings.HasSuffix(b.Flow.Callsites[from.CallsiteId].CalleeFqn, "WriteString") &&
			to.Kind == pb.VertexKind_CALL_ARG_PORT &&
			strings.HasSuffix(b.Flow.Callsites[to.CallsiteId].CalleeFqn, ").String") {
			found = true
		}
	}
	if !found {
		t.Error("Builder: WriteString's receiver port must write back into sb (edge to String's receiver)")
	}
	// json.Unmarshal(b, &t): `&t` is boxed into `any`; the write-back must peel
	// the MakeInterface and reach t.Name's use at sink
	if !writesBackToSink(build("example.com/libwrites.Unmarshal", true), "encoding/json.Unmarshal", 1) {
		t.Error("Unmarshal: port 1 must write back through &t to sink(t.Name)")
	}
	// off: none of it
	if writesBackToSink(build("example.com/libwrites.Unmarshal", false), "encoding/json.Unmarshal", 1) {
		t.Error("--library-writeback=false must emit no write-back edge")
	}
	// in-scope callee: never a library write-back (its summary's by-ref out,
	// under --byref-out, is the mechanism there)
	if writesBackToSink(build("example.com/libwrites.InScope", true), "libwrites.fill", 0) {
		t.Error("InScope: an in-scope callee must not get a library write-back edge")
	}
}
