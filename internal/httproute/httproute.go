// Package httproute recovers server-side HTTP routes from router registration
// calls (coverage wave 1 §2.2): which handler serves which method and path.
// Detection is registration-call analysis over SSA, never signature guessing —
// a func(w, r) that nothing registers is not a route.
//
// Three passes over the in-scope functions:
//
//  1. a router VALUE analysis: each router SSA value carries a small set of
//     (router object, prefix) facts. Objects are the constructor calls
//     (`chi.NewRouter()`); prefixes grow through Group/Route/PathPrefix,
//     flow through closures' router parameters (`r.Route("/v1", func(r
//     chi.Router){…})`), into in-scope functions' router parameters via the
//     call graph (`registerUsers(r)`), out through returns, and through local
//     variables and struct fields. Flow-insensitive and context-insensitive,
//     capped at maxFacts per value.
//  2. mounts: `r.Mount(p, sub)` and `mux.Handle(p, http.StripPrefix(p, sub))`
//     make every route of sub's object reachable under the parent's prefix
//     plus p; resolved to a fixpoint.
//  3. registrations: each registration call becomes one route per
//     (prefix, method), with its handler resolved through method values,
//     conversions, middleware wrappers and factories.
//
// The vendor shapes are rows of frameworks.RouterCalls.
package httproute

import (
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/ssa"

	"github.com/panoptiorg/panoptife-go/internal/frameworks"
	"github.com/panoptiorg/panoptife-go/internal/hash"
)

// Route is one served (method, path) and the function serving it.
type Route struct {
	IID       []byte // hash.ContractIID("http:<METHOD> <Path>")
	Method    string // upper-case; "*" = any
	Path      string // canonical (frameworks.CanonPath)
	Display   string // as composed from the source, holes as {}
	Framework string
	Handler   *ssa.Function
	// RequestParams are the handler's InParam indices (receiver excluded)
	// whose type carries the request (frameworks.RequestTypes).
	RequestParams []uint32
}

// Census is the `http-routes:` stderr line.
type Census struct {
	Routes             int
	ByFramework        map[string]int
	HandlersUnresolved int // registrations whose handler did not resolve to an emitted function
	PrefixesUnresolved int // registrations routed under an unknown prefix (kept as a suffix), or with no literal path
	EvalBudget         int // registrations whose path or prefix ran out of evaluator budget
}

// Dispatcher resolves dynamic call targets (flow.Dispatcher's method).
type Dispatcher interface {
	TargetsAt(site ssa.CallInstruction) ([]*ssa.Function, float32, bool)
}

const (
	// maxFacts caps the (object, prefix) facts per value (§2.2: "capped at 8").
	maxFacts = 8
	// maxAppends bounds prefix growth along a cycle (a router passed around a
	// recursive registration).
	maxAppends = 8
	// maxResolveDepth bounds handler resolution through wrappers.
	maxResolveDepth = 6
)

// fact: the value is router object obj at prefix rel below obj's root.
type fact struct {
	obj  int
	rel  frameworks.Str
	apps int
}

func (f fact) key() string { return f.rel.Template() }

type factSet struct {
	facts    []fact
	overflow bool
}

func (s *factSet) add(f fact) bool {
	for _, g := range s.facts {
		if g.obj == f.obj && g.key() == f.key() {
			return false
		}
	}
	if len(s.facts) >= maxFacts {
		s.overflow = true
		return false
	}
	s.facts = append(s.facts, f)
	return true
}

// cellKey is a struct field holding a router, program-wide (`s.router`).
type cellKey struct {
	obj   *types.TypeName
	field int
}

type redge struct {
	to  any // ssa.Value or cellKey
	app *frameworks.Str
	// slash: join app with path.Join semantics (frameworks.JoinSlash)
	slash bool
}

type registration struct {
	fn    *ssa.Function
	instr ssa.CallInstruction
	rc    *frameworks.RouterCall
	recv  ssa.Value
	args  []ssa.Value
}

type mount struct {
	recv   ssa.Value // nil: http.DefaultServeMux
	prefix frameworks.Str
	child  ssa.Value
	// strip: an http.StripPrefix mount. It strips from the request's URL
	// path, so the child serves under what was already stripped before the
	// parent plus the prefix — never under a group or PathPrefix the parent
	// routed through, which is still in the URL (review item 3). chi Mount
	// strips nothing from the URL; it routes the child under the parent's
	// full routing prefix.
	strip bool
}

// served is where a router object's root is reachable: its routing prefix
// (what its own routes are under), and the part of the URL path already
// stripped by StripPrefix mounts before it sees the request.
type served struct {
	route, stripped frameworks.Str
}

type analysis struct {
	prog      *ssa.Program
	disp      Dispatcher
	emittable func(*ssa.Function) bool
	inSet     map[*ssa.Function]bool

	edges   map[any][]redge
	facts   map[any]*factSet
	queue   []any
	objects []string // framework per router object
	defObj  int      // http.DefaultServeMux, -1 until used

	regs   []registration
	mounts []mount
	// where each mounted object is served; absent = a root ("")
	mounted map[int][]served
	// objects mounted under more places than maxFacts tracks
	mountOverflow map[int]bool

	handlerIface *types.Interface
}

// Extract runs the three passes over fns (the emitted functions, in emit's
// deterministic order) and returns the routes sorted by iid then handler.
func Extract(prog *ssa.Program, fns []*ssa.Function, disp Dispatcher, emittable func(*ssa.Function) bool) ([]Route, Census) {
	a := &analysis{
		prog:      prog,
		disp:      disp,
		emittable: emittable,
		inSet:     map[*ssa.Function]bool{},
		edges:     map[any][]redge{},
		facts:     map[any]*factSet{},
		defObj:    -1,
		mounted:   map[int][]served{},
	}
	for _, fn := range fns {
		a.inSet[fn] = true
	}
	if p := prog.ImportedPackage("net/http"); p != nil {
		if obj := p.Pkg.Scope().Lookup("Handler"); obj != nil {
			a.handlerIface, _ = obj.Type().Underlying().(*types.Interface)
		}
		// `http.DefaultServeMux.HandleFunc(..)` registers on the same root as
		// the package-level `http.HandleFunc(..)`.
		if g, ok := p.Members["DefaultServeMux"].(*ssa.Global); ok {
			a.seed(g, a.defaultMux())
		}
	}
	for _, fn := range fns {
		a.scan(fn)
	}
	a.propagate()
	a.stripMounts()
	a.resolveMounts()
	return a.routes()
}

func (a *analysis) defaultMux() fact {
	if a.defObj < 0 {
		a.defObj = len(a.objects)
		a.objects = append(a.objects, frameworks.NetHTTP)
	}
	return fact{obj: a.defObj, rel: frameworks.Lit("")}
}

func (a *analysis) seed(n any, f fact) {
	s := a.facts[n]
	if s == nil {
		s = &factSet{}
		a.facts[n] = s
	}
	if s.add(f) {
		a.queue = append(a.queue, n)
	}
}

func (a *analysis) edge(from, to any, app *frameworks.Str) {
	a.edges[from] = append(a.edges[from], redge{to: to, app: app})
}

// prefixEdge is an edge that appends a group prefix, joined the way the
// framework joins it.
func (a *analysis) prefixEdge(from, to any, app *frameworks.Str, framework string) {
	a.edges[from] = append(a.edges[from], redge{to: to, app: app, slash: frameworks.JoinSlash(framework)})
}

// routerish gates the analysis: only values that can hold a router get
// edges. Pointers are stripped all the way, so a router captured by reference
// (`**chi.Mux`) still counts.
func routerish(t types.Type) bool {
	for {
		p, ok := t.Underlying().(*types.Pointer)
		if !ok {
			break
		}
		if _, named := t.(*types.Named); named {
			break
		}
		t = p.Elem()
	}
	return frameworks.IsRouterType(t)
}

// ---------------------------------------------------------------------------
// pass 1: the router value graph
// ---------------------------------------------------------------------------

func (a *analysis) scan(fn *ssa.Function) {
	for _, blk := range fn.Blocks {
		for _, instr := range blk.Instrs {
			if call, ok := instr.(ssa.CallInstruction); ok {
				a.scanCall(fn, call)
			}
			switch it := instr.(type) {
			case *ssa.MakeClosure:
				cf, ok := it.Fn.(*ssa.Function)
				if !ok {
					continue
				}
				for i, bnd := range it.Bindings {
					if i < len(cf.FreeVars) && routerish(bnd.Type()) {
						a.edge(bnd, cf.FreeVars[i], nil)
					}
				}
			case *ssa.Store:
				if !routerish(it.Val.Type()) {
					continue
				}
				if fa, ok := it.Addr.(*ssa.FieldAddr); ok {
					if c, ok := cellOf(fa.X.Type(), fa.Field); ok {
						a.edge(it.Val, c, nil)
					}
					continue
				}
				a.edge(it.Val, it.Addr, nil)
			case *ssa.UnOp:
				if it.Op != token.MUL || !routerish(it.Type()) {
					continue
				}
				if fa, ok := it.X.(*ssa.FieldAddr); ok {
					if c, ok := cellOf(fa.X.Type(), fa.Field); ok {
						a.edge(c, it, nil)
					}
					continue
				}
				a.edge(it.X, it, nil)
			case *ssa.Field:
				if !routerish(it.Type()) {
					continue
				}
				if c, ok := cellOf(it.X.Type(), it.Field); ok {
					a.edge(c, it, nil)
				}
				if embedded(it.X.Type(), it.Field) {
					a.edge(it.X, it, nil)
				}
			case *ssa.FieldAddr:
				// gin: `engine.GET(..)` is (*RouterGroup).GET on
				// &engine.RouterGroup — the embedded router IS the engine.
				if routerish(it.Type()) && embedded(it.X.Type(), it.Field) {
					a.edge(it.X, it, nil)
				}
			case *ssa.MakeInterface:
				if routerish(it.Type()) {
					a.edge(it.X, it, nil)
				}
			case *ssa.ChangeInterface:
				if routerish(it.Type()) {
					a.edge(it.X, it, nil)
				}
			case *ssa.ChangeType:
				if routerish(it.Type()) {
					a.edge(it.X, it, nil)
				}
			case *ssa.TypeAssert:
				if routerish(it.AssertedType) {
					a.edge(it.X, it, nil)
				}
			case *ssa.Extract:
				if routerish(it.Type()) {
					a.edge(it.Tuple, it, nil)
				}
			case *ssa.Phi:
				if routerish(it.Type()) {
					for _, e := range it.Edges {
						a.edge(e, it, nil)
					}
				}
			}
		}
	}
}

func (a *analysis) scanCall(fn *ssa.Function, call ssa.CallInstruction) {
	cc := call.Common()
	val := call.Value() // nil for go/defer
	if rc, recv, args, ok := frameworks.MatchRouterCall(cc); ok {
		arg := func(i int) ssa.Value {
			if i >= 0 && i < len(args) {
				return args[i]
			}
			return nil
		}
		prefix := func() *frameworks.Str {
			if rc.PathArg < 0 || arg(rc.PathArg) == nil {
				return nil
			}
			s := frameworks.Eval{}.Of(arg(rc.PathArg))
			return &s
		}
		switch rc.Op {
		case frameworks.OpNew:
			if val != nil {
				obj := len(a.objects)
				a.objects = append(a.objects, rc.Framework)
				a.seed(val, fact{obj: obj, rel: frameworks.Lit("")})
			}
		case frameworks.OpRoute:
			a.regs = append(a.regs, registration{fn: fn, instr: call, rc: rc, recv: recv, args: args})
		case frameworks.OpGroup:
			if val != nil && recv != nil {
				a.prefixEdge(recv, val, prefix(), rc.Framework)
			}
		case frameworks.OpSame:
			if val != nil && recv != nil {
				a.edge(recv, val, nil)
			}
		case frameworks.OpSub:
			p := prefix()
			if sub := funcOf(arg(rc.FnArg)); sub != nil && len(sub.Params) > 0 && recv != nil {
				a.prefixEdge(recv, sub.Params[0], p, rc.Framework)
			}
			if val != nil && recv != nil {
				a.prefixEdge(recv, val, p, rc.Framework)
			}
		case frameworks.OpMount:
			if p := prefix(); p != nil && arg(rc.HandlerArg) != nil {
				a.mounts = append(a.mounts, mount{recv: recv, prefix: *p, child: arg(rc.HandlerArg)})
			}
		}
		return
	}

	// An ordinary call: router arguments flow into the callee's parameters
	// and router results back out — `registerUsers(r)`, `return r`.
	var targets []*ssa.Function
	if sc := cc.StaticCallee(); sc != nil {
		if a.inSet[sc] {
			targets = []*ssa.Function{sc}
		}
	} else if a.disp != nil {
		for _, t := range targetsOf(a.disp, call) {
			if a.inSet[t] {
				targets = append(targets, t)
			}
		}
	}
	actuals := cc.Args
	if cc.IsInvoke() {
		actuals = append([]ssa.Value{cc.Value}, cc.Args...)
	}
	for _, t := range targets {
		for i, act := range actuals {
			if i < len(t.Params) && routerish(t.Params[i].Type()) {
				a.edge(act, t.Params[i], nil)
			}
		}
		if val == nil {
			continue
		}
		res := t.Signature.Results()
		for _, blk := range t.Blocks {
			ret, ok := blk.Instrs[len(blk.Instrs)-1].(*ssa.Return)
			if !ok {
				continue
			}
			for j, r := range ret.Results {
				if !routerish(res.At(j).Type()) {
					continue
				}
				if res.Len() == 1 {
					a.edge(r, val, nil)
					continue
				}
				for _, ex := range extracts(val, j) {
					a.edge(r, ex, nil)
				}
			}
		}
	}
}

func targetsOf(d Dispatcher, call ssa.CallInstruction) []*ssa.Function {
	ts, _, _ := d.TargetsAt(call)
	return ts
}

func extracts(tuple ssa.Value, j int) []ssa.Value {
	var out []ssa.Value
	if refs := tuple.Referrers(); refs != nil {
		for _, r := range *refs {
			if ex, ok := r.(*ssa.Extract); ok && ex.Index == j {
				out = append(out, ex)
			}
		}
	}
	return out
}

// funcOf: the function a func-typed value denotes (a func literal or a
// closure over one).
func funcOf(v ssa.Value) *ssa.Function {
	switch v := v.(type) {
	case *ssa.Function:
		return v
	case *ssa.MakeClosure:
		f, _ := v.Fn.(*ssa.Function)
		return f
	case *ssa.ChangeType:
		return funcOf(v.X)
	}
	return nil
}

func cellOf(t types.Type, field int) (cellKey, bool) {
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	if !ok {
		return cellKey{}, false
	}
	return cellKey{obj: n.Origin().Obj(), field: field}, true
}

func embedded(t types.Type, field int) bool {
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	st, ok := t.Underlying().(*types.Struct)
	return ok && field < st.NumFields() && st.Field(field).Embedded()
}

func (a *analysis) propagate() {
	for len(a.queue) > 0 {
		n := a.queue[0]
		a.queue = a.queue[1:]
		src := a.facts[n]
		for _, e := range a.edges[n] {
			dst := a.facts[e.to]
			if dst == nil {
				dst = &factSet{}
				a.facts[e.to] = dst
			}
			before := len(dst.facts)
			wasOver := dst.overflow
			// an overflowed value has lost facts; everything it reaches has
			if src.overflow {
				dst.overflow = true
			}
			for _, f := range src.facts {
				if e.app != nil {
					if f.apps >= maxAppends {
						dst.overflow = true
						continue
					}
					f = fact{obj: f.obj, rel: frameworks.JoinPath(f.rel, *e.app, e.slash), apps: f.apps + 1}
				}
				dst.add(f)
			}
			if len(dst.facts) != before || dst.overflow != wasOver {
				a.queue = append(a.queue, e.to)
			}
		}
	}
}

func (a *analysis) factsOf(v ssa.Value) *factSet {
	if v == nil {
		return nil
	}
	return a.facts[v]
}

// routerIn finds the router a handler-typed value carries: the value itself,
// or a router wrapped by middleware (`logging(r)`, `cors.Handler(r)`), so a
// wrapped sub-router is still mounted (or delegated to) rather than taken for
// a handler. nil: no router.
func (a *analysis) routerIn(v ssa.Value, depth int) ssa.Value {
	if v == nil || depth > maxResolveDepth {
		return nil
	}
	if len(a.factsOf(v).get()) > 0 {
		return v
	}
	if c, ok := v.(*ssa.Call); ok {
		if _, _, ok := stripPrefix(c); ok {
			return nil // a StripPrefix is a mount, handled by the caller
		}
		for _, arg := range c.Call.Args {
			if frameworks.IsHandlerShaped(arg.Type(), a.handlerIface) {
				if r := a.routerIn(arg, depth+1); r != nil {
					return r
				}
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// pass 2: mounts
// ---------------------------------------------------------------------------

// stripMounts turns `mux.Handle("/api/", http.StripPrefix("/api", sub))` into
// a mount of sub's routes under /api when sub is a router. Registrations of a
// router WITHOUT StripPrefix are delegation: the sub-router sees the full
// path, so its own routes already carry it.
func (a *analysis) stripMounts() {
	for _, r := range a.regs {
		h := handlerArg(r)
		if h == nil {
			continue
		}
		if p, inner, ok := stripPrefix(h); ok {
			if sub := a.routerIn(inner, 0); sub != nil {
				recv := r.recv
				if r.rc.DefaultMux {
					recv = nil
				}
				a.mounts = append(a.mounts, mount{recv: recv, prefix: p, child: sub, strip: true})
			}
		}
	}
}

func (s *factSet) get() []fact {
	if s == nil {
		return nil
	}
	return s.facts
}

func stripPrefix(v ssa.Value) (frameworks.Str, ssa.Value, bool) {
	c, ok := v.(*ssa.Call)
	if !ok {
		return frameworks.Str{}, nil, false
	}
	rc, _, args, ok := frameworks.MatchRouterCall(&c.Call)
	if !ok || rc.Op != frameworks.OpStrip || len(args) <= rc.HandlerArg {
		return frameworks.Str{}, nil, false
	}
	return frameworks.Eval{}.Of(args[rc.PathArg]), args[rc.HandlerArg], true
}

// resolveMounts computes where each mounted object is served. Every round
// recomputes all of them from the previous round's — a parent's prefixes
// change once IT is found to be mounted, and "served at the root" must not
// survive that — until nothing changes (mount trees are shallow; the round
// cap only matters for a mount cycle). A mount under a parent of unknown
// origin is an unresolved prefix, kept as a hole so the route is emitted as
// a suffix.
func (a *analysis) resolveMounts() {
	for round := 0; round < 16; round++ {
		next := map[int][]served{}
		over := map[int]bool{}
		add := func(obj int, s served) {
			for _, q := range next[obj] {
				if q.route.Equal(s.route) && q.stripped.Equal(s.stripped) {
					return
				}
			}
			if len(next[obj]) >= maxFacts {
				over[obj] = true
				return
			}
			next[obj] = append(next[obj], s)
		}
		for _, m := range a.mounts {
			var parents []fact
			if m.recv == nil {
				parents = []fact{a.defaultMux()}
			} else {
				parents = a.factsOf(m.recv).get()
			}
			for _, child := range a.factsOf(a.routerIn(m.child, 0)).get() {
				if len(parents) == 0 {
					u := frameworks.JoinPath(frameworks.Unknown(frameworks.CauseOther), m.prefix, false)
					add(child.obj, served{route: u, stripped: u})
					continue
				}
				for _, p := range parents {
					for _, at := range a.servedAt(p.obj) {
						if m.strip {
							s := frameworks.JoinPath(at.stripped, m.prefix, false)
							add(child.obj, served{route: s, stripped: s})
						} else {
							r := frameworks.JoinPath(frameworks.JoinPath(at.route, p.rel, false), m.prefix, false)
							add(child.obj, served{route: r, stripped: at.stripped})
						}
					}
				}
			}
		}
		if sameMounts(a.mounted, next) {
			a.mountOverflow = over
			return
		}
		a.mounted, a.mountOverflow = next, over
	}
}

func sameMounts(a, b map[int][]served) bool {
	if len(a) != len(b) {
		return false
	}
	for k, xs := range a {
		ys := b[k]
		if len(xs) != len(ys) {
			return false
		}
		for i := range xs {
			if !xs[i].route.Equal(ys[i].route) || !xs[i].stripped.Equal(ys[i].stripped) {
				return false
			}
		}
	}
	return true
}

// servedAt: where object obj's root is served — its mounts, or the root.
func (a *analysis) servedAt(obj int) []served {
	if ms := a.mounted[obj]; len(ms) > 0 {
		return ms
	}
	return []served{{}}
}

// ---------------------------------------------------------------------------
// pass 3: registrations -> routes
// ---------------------------------------------------------------------------

func handlerArg(r registration) ssa.Value {
	if r.rc.HandlerArg < 0 || r.rc.HandlerArg >= len(r.args) {
		return nil
	}
	h := r.args[r.rc.HandlerArg]
	if r.rc.LastHandler {
		// gin: middleware first, the endpoint last (§2.2).
		elems, ok := frameworks.SliceElems(h)
		if !ok || len(elems) == 0 {
			return nil
		}
		return elems[len(elems)-1]
	}
	return h
}

func (a *analysis) routes() ([]Route, Census) {
	cen := Census{ByFramework: map[string]int{}}
	seen := map[string]bool{}
	var out []Route
	for _, r := range a.regs {
		h := handlerArg(r)
		if h == nil {
			cen.HandlersUnresolved++
			continue
		}
		// a router handed to a registration is a mount (StripPrefix, done
		// above) or delegation: no route of its own
		if a.routerIn(h, 0) != nil {
			continue
		}
		if _, inner, ok := stripPrefix(h); ok {
			if a.routerIn(inner, 0) != nil {
				continue
			}
			h = inner
		}
		hfn := a.resolveHandler(h, 0)
		if hfn == nil || !a.emittable(hfn) {
			cen.HandlersUnresolved++
			continue
		}

		pattern := frameworks.Lit("")
		if r.rc.PathArg >= 0 && r.rc.PathArg < len(r.args) {
			pattern = frameworks.Eval{}.Of(r.args[r.rc.PathArg])
		}
		methods := a.methodsOf(r)
		if r.rc.PatternMethod {
			var m string
			m, pattern = frameworks.SplitPattern(pattern)
			if m != "" {
				methods = []string{strings.ToUpper(m)}
			}
			// net/http: "/static/" serves its whole subtree
			pattern = frameworks.Subtree(pattern)
		}
		budget := pattern.Exhausted()

		var prefixes []frameworks.Str
		prefixUnknown := false // set below, per prefix, when one is a hole
		var recvFacts *factSet
		if r.rc.DefaultMux {
			recvFacts = &factSet{facts: []fact{a.defaultMux()}}
		} else {
			recvFacts = a.factsOf(r.recv)
		}
		overflow := recvFacts != nil && recvFacts.overflow
		for _, f := range recvFacts.get() {
			budget = budget || f.rel.Exhausted()
			for _, at := range a.servedAt(f.obj) {
				prefixes = append(prefixes, frameworks.JoinPath(at.route, f.rel, false))
			}
			overflow = overflow || a.mountOverflow[f.obj]
		}
		// No known origin, or more origins than the analysis keeps: the
		// route is still emitted, under an unknown prefix — its known suffix
		// is what the core's suffix match works with (review item 9).
		if len(prefixes) == 0 || overflow {
			prefixes = append(prefixes, frameworks.Unknown(frameworks.CauseOther))
		}
		if budget {
			cen.EvalBudget++
		}

		// A path that is nothing but a hole (`r.Get(cfg.Path, h)`) names no
		// route at all: emitting "/{}" would match any one-segment client.
		if !pattern.HasLiteral() && pattern.Template() != "" {
			cen.PrefixesUnresolved++
			continue
		}
		params := requestParams(hfn)
		for _, p := range prefixes {
			full := frameworks.JoinPath(p, pattern, frameworks.JoinSlash(r.rc.Framework))
			if full.LeadingHole() {
				// an unresolved prefix is dropped: the route keeps the known
				// suffix, which the core's suffix match is built to absorb
				prefixUnknown = true
				full = full.TrimLeadingHole()
			}
			tmpl := full.Template()
			canon := frameworks.CanonPath(tmpl)
			for _, m := range methods {
				name := frameworks.ContractName(m, canon)
				key := name + "\x00" + hash.FQN(hfn)
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, Route{
					IID:           hash.ContractIID(name),
					Method:        m,
					Path:          canon,
					Display:       display(tmpl),
					Framework:     r.rc.Framework,
					Handler:       hfn,
					RequestParams: params,
				})
				cen.ByFramework[r.rc.Framework]++
			}
		}
		if prefixUnknown {
			cen.PrefixesUnresolved++
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if c := strings.Compare(string(out[i].IID), string(out[j].IID)); c != 0 {
			return c < 0
		}
		return hash.FQN(out[i].Handler) < hash.FQN(out[j].Handler)
	})
	cen.Routes = len(out)
	return out, cen
}

// display is the path as composed, for reporting: doubled slashes from
// prefix joins collapsed, "/" for the root.
func display(t string) string {
	for strings.Contains(t, "//") {
		t = strings.ReplaceAll(t, "//", "/")
	}
	if !strings.HasPrefix(t, "/") {
		t = "/" + t
	}
	return t
}

// methodsOf: the HTTP methods one registration serves, "*" for any.
func (a *analysis) methodsOf(r registration) []string {
	rc := r.rc
	switch {
	case rc.Method != "" && rc.Method != "*" && !rc.RouteChain:
		return []string{rc.Method}
	case rc.MethodArg >= 0 && rc.MethodArg < len(r.args):
		if m, ok := (frameworks.Eval{}).Of(r.args[rc.MethodArg]).Known(); ok && m != "" {
			return []string{strings.ToUpper(m)}
		}
		return []string{"*"}
	case rc.MethodsArg >= 0 && rc.MethodsArg < len(r.args):
		if ms := constStrings(r.args[rc.MethodsArg]); len(ms) > 0 {
			return ms
		}
		return []string{"*"}
	case rc.RouteChain:
		if ms := a.routeChainMethods(r); len(ms) > 0 {
			return ms
		}
	}
	return []string{"*"}
}

// constStrings: the upper-cased constant elements of a []string literal or
// variadic argument; nil when any element is not constant.
func constStrings(v ssa.Value) []string {
	elems, ok := frameworks.SliceElems(v)
	if !ok {
		return nil
	}
	var out []string
	for _, e := range elems {
		s, ok := frameworks.Eval{}.Of(e).Known()
		if !ok {
			return nil
		}
		out = append(out, strings.ToUpper(s))
	}
	return out
}

// routeChainMethods collects gorilla `.Methods(..)` along the *Route chain of
// a registration: after it (`r.HandleFunc(..).Methods("GET")`) and before it
// (`r.Methods("GET").Path("/x").HandlerFunc(h)`). Bounded, local def-use.
func (a *analysis) routeChainMethods(r registration) []string {
	set := map[string]bool{}
	addFrom := func(cc *ssa.CallCommon) (*frameworks.RouterCall, ssa.Value, bool) {
		rc, recv, args, ok := frameworks.MatchRouterCall(cc)
		if !ok || rc.Framework != r.rc.Framework {
			return nil, nil, false
		}
		if rc.MethodsArg >= 0 && rc.MethodsArg < len(args) {
			for _, m := range constStrings(args[rc.MethodsArg]) {
				set[m] = true
			}
		}
		return rc, recv, true
	}
	// forward from the registration's result
	frontier := []ssa.Value{r.instr.Value()}
	for depth := 0; depth < 8 && len(frontier) > 0; depth++ {
		var next []ssa.Value
		for _, v := range frontier {
			if v == nil || v.Referrers() == nil {
				continue
			}
			for _, ref := range *v.Referrers() {
				c, ok := ref.(*ssa.Call)
				if !ok {
					continue
				}
				if rc, recv, ok := addFrom(&c.Call); ok && recv == v &&
					(rc.Op == frameworks.OpSame || rc.Op == frameworks.OpGroup) {
					next = append(next, c)
				}
			}
		}
		frontier = next
	}
	// backward through the receiver's defining calls
	v := r.recv
	for depth := 0; depth < 8 && v != nil; depth++ {
		c, ok := v.(*ssa.Call)
		if !ok {
			break
		}
		rc, recv, ok := addFrom(&c.Call)
		if !ok || (rc.Op != frameworks.OpSame && rc.Op != frameworks.OpGroup) {
			break
		}
		v = recv
	}
	var out []string
	for m := range set {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// resolveHandler finds the function a handler argument denotes (§2.2):
// a function; a closure, with a bound method value resolved to the method;
// an http.HandlerFunc conversion; a value implementing http.Handler, to its
// ServeHTTP; a middleware call `mw(h)`, to the handler argument h; a factory
// call returning a closure, to that closure (one level).
func (a *analysis) resolveHandler(v ssa.Value, depth int) *ssa.Function {
	if v == nil || depth > maxResolveDepth {
		return nil
	}
	switch x := v.(type) {
	case *ssa.Function:
		return x
	case *ssa.MakeClosure:
		fn, ok := x.Fn.(*ssa.Function)
		if !ok {
			return nil
		}
		if strings.HasPrefix(fn.Synthetic, "bound method wrapper") {
			if m, ok := fn.Object().(*types.Func); ok {
				return a.prog.FuncValue(m) // nil for an interface method value
			}
		}
		return fn
	case *ssa.ChangeType:
		return a.resolveHandler(x.X, depth+1)
	case *ssa.ChangeInterface:
		return a.resolveHandler(x.X, depth+1)
	case *ssa.MakeInterface:
		if _, isFunc := x.X.Type().Underlying().(*types.Signature); isFunc {
			return a.resolveHandler(x.X, depth+1)
		}
		return a.serveHTTP(x.X.Type())
	case *ssa.UnOp:
		if lv := frameworks.LocalValue(x); lv != nil {
			return a.resolveHandler(lv, depth+1)
		}
	case *ssa.Phi:
		var out *ssa.Function
		for _, e := range x.Edges {
			f := a.resolveHandler(e, depth+1)
			if f == nil || (out != nil && f != out) {
				return nil
			}
			out = f
		}
		return out
	case *ssa.Call:
		if _, inner, ok := stripPrefix(x); ok {
			return a.resolveHandler(inner, depth+1)
		}
		// middleware: the handler it wraps — only an argument that IS a
		// handler (frameworks.HandlerShapes, or an http.Handler). Any other
		// func-typed argument is a dependency of a factory
		// (`makeHandler(encodeJSON)`), not the endpoint (review item 1).
		for _, arg := range x.Call.Args {
			if frameworks.IsHandlerShaped(arg.Type(), a.handlerIface) {
				if f := a.resolveHandler(arg, depth+1); f != nil {
					return f
				}
			}
		}
		// factory: every return of the callee is the same closure
		if sc := x.Call.StaticCallee(); sc != nil && a.inSet[sc] {
			var out *ssa.Function
			for _, blk := range sc.Blocks {
				ret, ok := blk.Instrs[len(blk.Instrs)-1].(*ssa.Return)
				if !ok || len(ret.Results) == 0 {
					continue
				}
				f := a.closureOf(ret.Results[0])
				if f == nil || (out != nil && f != out) {
					return nil
				}
				out = f
			}
			return out
		}
	}
	return nil
}

// closureOf is resolveHandler restricted to values that need no further
// call: the "one level" of the factory rule.
func (a *analysis) closureOf(v ssa.Value) *ssa.Function {
	for i := 0; i < 4; i++ {
		switch x := v.(type) {
		case *ssa.ChangeType:
			v = x.X
		case *ssa.MakeInterface:
			v = x.X
		case *ssa.Function, *ssa.MakeClosure:
			return a.resolveHandler(x, maxResolveDepth)
		default:
			return nil
		}
	}
	return nil
}

// serveHTTP: the ServeHTTP method of a concrete http.Handler, looked up
// without building anything (FuncValue is a plain lookup for a declared
// method).
func (a *analysis) serveHTTP(t types.Type) *ssa.Function {
	if types.IsInterface(t) {
		return nil
	}
	obj, _, _ := types.LookupFieldOrMethod(t, true, nil, "ServeHTTP")
	m, ok := obj.(*types.Func)
	if !ok {
		return nil
	}
	return a.prog.FuncValue(m)
}

// requestParams: the handler's InParam indices (receiver excluded) whose
// type carries the request — `r` of func(w, r) is [1], gin's `c` is [0].
func requestParams(fn *ssa.Function) []uint32 {
	var out []uint32
	ps := fn.Signature.Params()
	for i := 0; i < ps.Len(); i++ {
		if frameworks.IsRequestType(ps.At(i).Type()) {
			out = append(out, uint32(i))
		}
	}
	return out
}
