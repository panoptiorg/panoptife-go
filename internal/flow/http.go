package flow

import (
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"

	pb "github.com/panoptiorg/panoptife-go/internal/cgfpb"
	"github.com/panoptiorg/panoptife-go/internal/frameworks"
)

// Coverage wave 1 §2.1 and §2.3: two kinds of synthetic call site, both
// appended after every real one (and after the closure bindings), because
// cgstore's snapshot replay keys dispatch on the ordinal of a real
// ssa.CallInstruction — ids 0..NCallInstrs-1 must keep meaning what they mean
// with the flags off (see emitClosureBindings).

// surfaceRead is a load of an untrusted-input field (§2.1).
type surfaceRead struct {
	val ssa.Value // the loaded value: the *ssa.UnOp load or the *ssa.Field
	pos token.Pos
	fqn string // read:<import path>.<Type>.<Field>
}

// clientCall is a call to an HTTP client entry point (§2.3).
type clientCall struct {
	instr ssa.CallInstruction
	call  *frameworks.ClientCall
	args  []ssa.Value // receiver excluded, as the table indexes them
}

// surfaceHook records the surface reads of one instruction. No-op unless
// --surface-reads; additive like heapHook, so the switch in scanInstrs still
// wires the load's operand edge (`r -> r.Body`) exactly as before.
func (b *builder) surfaceHook(instr ssa.Instruction) {
	if !b.opts.SurfaceReads {
		return
	}
	switch it := instr.(type) {
	case *ssa.UnOp:
		// `r.Body` on a *Request is FieldAddr + load; only the LOAD is a read
		// (`r.Body = x` stores through the same FieldAddr).
		if it.Op != token.MUL {
			return
		}
		if fa, ok := it.X.(*ssa.FieldAddr); ok {
			if fqn, ok := frameworks.SurfaceRead(fa.X.Type(), fa.Field); ok && !outgoingRequest(fa.X, 0) {
				// the load itself has no position; the selector does
				b.reads = append(b.reads, surfaceRead{val: it, pos: fa.Pos(), fqn: fqn})
			}
		}
	case *ssa.Field:
		if fqn, ok := frameworks.SurfaceRead(it.X.Type(), it.Field); ok && !outgoingRequest(it.X, 0) {
			b.reads = append(b.reads, surfaceRead{val: it, pos: it.Pos(), fqn: fqn})
		}
	}
}

// outgoingRequest reports that request value v provably comes, within this
// function, from a request this process builds to SEND
// (frameworks.OutgoingRequests): `req.Header.Set(..)` on one is not request
// input, and a `read:` source there is a false positive (review item 12).
// Anything not proven outgoing — a parameter, a field of one, an accessor's
// result like echo's c.Request() — keeps its read: site.
func outgoingRequest(v ssa.Value, depth int) bool {
	if depth > 8 {
		return false
	}
	og := frameworks.OutgoingRequests
	switch x := v.(type) {
	case *ssa.Extract:
		if c, ok := x.Tuple.(*ssa.Call); ok && x.Index == 0 {
			return outgoingRequest(c, depth+1)
		}
	case *ssa.Call:
		for _, oc := range og.Calls {
			if frameworks.MatchCallee(&x.Call, []string{oc.Pkg}, oc.Recv, oc.Name) {
				return true
			}
		}
		for _, m := range og.Derive {
			if frameworks.MatchCallee(&x.Call, []string{og.Literal[0]}, og.Literal[1], m) && len(x.Call.Args) > 0 {
				return outgoingRequest(x.Call.Args[0], depth+1) // a copy keeps its origin
			}
		}
	case *ssa.Alloc:
		// `&http.Request{…}` (or a local request variable): built here
		pkg, name, _ := namedStructOf(x.Type())
		return pkg == og.Literal[0] && name == og.Literal[1]
	case *ssa.UnOp:
		if lv := frameworks.LocalValue(x); lv != nil {
			return outgoingRequest(lv, depth+1)
		}
		if fa, ok := x.X.(*ssa.FieldAddr); ok {
			return isResponseRequest(fa.X.Type(), fa.Field)
		}
	case *ssa.Field:
		return isResponseRequest(x.X.Type(), x.Field)
	case *ssa.Phi:
		for _, e := range x.Edges {
			if !outgoingRequest(e, depth+1) {
				return false
			}
		}
		return len(x.Edges) > 0
	}
	return false
}

// isResponseRequest: field `field` of t is *http.Response's Request — the
// request a client call sent.
func isResponseRequest(t types.Type, field int) bool {
	rf := frameworks.OutgoingRequests.ResponseField
	pkg, name, st := namedStructOf(t)
	return st != nil && pkg == rf[0] && name == rf[1] && field < st.NumFields() && st.Field(field).Name() == rf[2]
}

func namedStructOf(t types.Type) (pkg, name string, st *types.Struct) {
	s, n := structOf(t)
	if s == nil || n == nil || n.Obj().Pkg() == nil {
		return "", "", nil
	}
	return n.Obj().Pkg().Path(), n.Obj().Name(), s
}

// emitSurfaceReads materializes each read as a synthetic zero-arg STATIC
// call site, opaque, with one result port that is a SOURCE of the loaded
// value — the shape a catalog `[[sources]]` rule fires on, so
// `read:net/http.Request.Body` can be named like any accessor call. The port
// goes into `ordered` as a real call's port 0 does: `r.URL.Path` reads a field
// of the result, and a whole-object source is cut at the first projection
// unless a path variant exists for it (see emitAccessVariants).
func (b *builder) emitSurfaceReads() {
	for _, rd := range b.reads {
		csID := uint32(len(b.flow.Callsites))
		v := b.addVertex(pb.VertexKind_CALL_RESULT_PORT, 0, csID, "", nil)
		b.srcVerts[rd.val] = append(b.srcVerts[rd.val], v)
		b.ordered = append(b.ordered, srcEntry{rd.val, v})
		b.flow.Callsites = append(b.flow.Callsites, &pb.CallSite{
			Id:                 csID,
			Kind:               pb.CallSite_STATIC,
			DispatchConfidence: 1.0,
			CalleeFqn:          rd.fqn,
			Argc:               0,
			Resultc:            1,
			Opaque:             true,
			Span:               b.posSpan(rd.pos),
		})
		b.callees = append(b.callees, Callee{FQN: rd.fqn, Kind: pb.CallSite_STATIC})
	}
}

// emitHTTPCalls materializes each client call as the §2.3 synthetic site:
// STATIC, opaque, argc 1, resultc 0, `callee_fqn = "http:<METHOD> <path>"`,
// HttpCall set, and every data-bearing argument (URL, body, form) flowing into
// its single arg port 0. resultc 0 is what makes an unlinked site inert: the
// core's default leaf taints results only, and there are none. The ordinary
// site was emitted untouched, so the catalog's egress rules still fire on it.
func (b *builder) emitHTTPCalls() {
	for _, c := range b.clients {
		method := c.call.Method
		exhausted := false
		if c.call.MethodArg >= 0 && c.call.MethodArg < len(c.args) {
			ms := frameworks.Eval{}.Of(c.args[c.call.MethodArg])
			m, ok := ms.Known()
			method = ""
			if ok {
				method = strings.ToUpper(m)
			}
			exhausted = ms.Exhausted()
		}
		url := frameworks.Unknown(frameworks.CauseOther)
		if c.call.URLArg < len(c.args) {
			url = frameworks.Eval{}.Of(c.args[c.call.URLArg])
		}
		if exhausted || url.Exhausted() {
			b.httpC.EvalBudget++
		}
		path := frameworks.CanonPath(frameworks.ClientPath(url))

		b.httpC.Sites++
		if method == "" {
			b.httpC.UnknownMethod++
		}
		if url.LeadingHole() {
			b.httpC.DynamicBase++
		}
		if hasLiteralSegment(path) {
			b.httpC.ResolvedPath++
		}

		csID := uint32(len(b.flow.Callsites))
		typ := ""
		if c.call.URLArg < len(c.args) {
			typ = b.typeStr(c.args[c.call.URLArg].Type())
		}
		port := b.addVertex(pb.VertexKind_CALL_ARG_PORT, 0, csID, typ, nil)
		for _, i := range c.call.DataArgs {
			if i < len(c.args) {
				a := c.args[i]
				b.sinkVerts[a] = append(b.sinkVerts[a], port)
			}
		}
		// An unknown method reads as "any" in the name, as in the TS frontend;
		// HttpCall.method stays "" so the core can tell unknown from a route's "*".
		fqnMethod := method
		if fqnMethod == "" {
			fqnMethod = "*"
		}
		fqn := "http:" + fqnMethod + " " + path
		b.flow.Callsites = append(b.flow.Callsites, &pb.CallSite{
			Id:                 csID,
			Kind:               pb.CallSite_STATIC,
			DispatchConfidence: 1.0,
			CalleeFqn:          fqn,
			Argc:               1,
			Resultc:            0,
			Opaque:             true,
			Span:               b.span(c.instr),
			HttpCall:           &pb.HttpCall{Method: method, Path: path},
		})
		b.callees = append(b.callees, Callee{FQN: fqn, Kind: pb.CallSite_STATIC})
	}
}

// hasLiteralSegment: the canonical path names at least one fixed segment —
// the census's "resolved_path".
func hasLiteralSegment(canon string) bool {
	for _, seg := range strings.Split(canon, "/") {
		if seg != "" && seg != frameworks.Hole && seg != frameworks.CatchAll {
			return true
		}
	}
	return false
}
