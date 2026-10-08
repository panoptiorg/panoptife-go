package httproute

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/panoptiorg/panoptife-go/internal/callgraph"
	"github.com/panoptiorg/panoptife-go/internal/frameworks"
	"github.com/panoptiorg/panoptife-go/internal/hash"
	"github.com/panoptiorg/panoptife-go/internal/loader"
)

// extract loads dir and runs Extract the way emit does: over the in-scope
// functions with bodies, sorted, with a live VTA resolver.
func extract(t *testing.T, dir string) ([]Route, Census) {
	t.Helper()
	ld, err := loader.Load(dir, "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("load %s: %v", dir, err)
	}
	inScope := map[*ssa.Package]bool{}
	for _, p := range ld.InitPkgs {
		inScope[p] = true
	}
	emittable := func(fn *ssa.Function) bool {
		if fn == nil || len(fn.Blocks) == 0 {
			return false
		}
		if fn.Pkg != nil {
			return inScope[fn.Pkg]
		}
		for f := fn; f != nil; f = f.Parent() {
			if obj := f.Object(); obj != nil && obj.Pkg() != nil {
				return inScope[ld.Prog.Package(obj.Pkg())]
			}
		}
		return false
	}
	var fns []*ssa.Function
	for fn := range ssautil.AllFunctions(ld.Prog) {
		if emittable(fn) {
			fns = append(fns, fn)
		}
	}
	sort.Slice(fns, func(i, j int) bool { return fns[i].String() < fns[j].String() })
	cg, _ := loader.BuildCallGraph(ld.Prog, "vta")
	return Extract(ld.Prog, fns, callgraph.NewResolver(cg, callgraph.DefaultFanoutCap, emittable), emittable)
}

// rows renders routes as "METHOD path framework params handler", sorted.
func rows(rs []Route) []string {
	var out []string
	for _, r := range rs {
		out = append(out, fmt.Sprintf("%s %s %s %v %s", r.Method, r.Path, r.Framework, r.RequestParams, r.Handler))
	}
	sort.Strings(out)
	return out
}

func diffRows(t *testing.T, got, want []string) {
	t.Helper()
	g, w := map[string]bool{}, map[string]bool{}
	for _, r := range got {
		g[r] = true
	}
	for _, r := range want {
		w[r] = true
		if !g[r] {
			t.Errorf("missing route: %s", r)
		}
	}
	for _, r := range got {
		if !w[r] {
			t.Errorf("unexpected route: %s", r)
		}
	}
}

// The coverage wave 1 §5 "routes" acceptance: every route of the five
// routers in fixtures/httpapi, method and canonical path exactly, mounted the
// way main mounts them (chi under StripPrefix("/chi"), the others carrying
// their own prefix), each bound to the method a method value names.
func TestHTTPAPIFixtureRoutes(t *testing.T) {
	routes, cen := extract(t, "../../fixtures/httpapi")
	const p = "example.com/httpapi/"
	want := []string{
		// net/http 1.22 patterns, the mux handed in from main
		"POST /api/users net/http [1] (*" + p + "stdapi.Server).createUser",
		"GET /api/users/{} net/http [1] (*" + p + "stdapi.Server).getUser",
		"GET /api/stats net/http [1] (*" + p + "stdapi.Server).stats",
		"GET /files/{*} net/http [1] (*" + p + "stdapi.Server).file",
		// chi: Route + register function, Group, Mount, With, Method
		"POST /chi/v1/orders chi [1] (*" + p + "chiapi.Handler).CreateOrder",
		"GET /chi/v1/orders/{} chi [1] (*" + p + "chiapi.Handler).GetOrder",
		"DELETE /chi/v1/orders/{} chi [1] (*" + p + "chiapi.Handler).DeleteOrder",
		"GET /chi/admin/audit chi [1] (*" + p + "chiapi.Handler).Audit",
		"PUT /chi/admin/flags/{} chi [1] (*" + p + "chiapi.Handler).SetFlag",
		"GET /chi/health chi [1] " + p + "chiapi.health",
		// gin: engine (promoted RouterGroup), nested groups through a param,
		// middleware ahead of the handler
		"POST /gin/items gin [0] (" + p + "ginapi.itemHandler).create",
		"GET /gin/items/{} gin [0] (" + p + "ginapi.itemHandler).get",
		"GET /gin/ping gin [0] " + p + "ginapi.New$1",
		// gorilla: subrouter, methods after and before the path
		"GET /gorilla/notes/{} gorilla [1] (*" + p + "gorillaapi.notes).get",
		"POST /gorilla/notes gorilla [1] (*" + p + "gorillaapi.notes).create",
		"PUT /gorilla/notes gorilla [1] (*" + p + "gorillaapi.notes).create",
		"DELETE /gorilla/notes/{} gorilla [1] (*" + p + "gorillaapi.notes).remove",
		// echo: group, and a closure with trailing middleware
		"GET /echo/greet/{} echo [0] " + p + "echoapi.greet",
		"POST /echo/search echo [0] " + p + "echoapi.New$1",
	}
	diffRows(t, rows(routes), want)
	if cen.Routes != len(want) || cen.HandlersUnresolved != 0 || cen.PrefixesUnresolved != 0 {
		t.Errorf("census = %+v, want %d routes and nothing unresolved", cen, len(want))
	}
	wantFw := map[string]int{"net/http": 4, "chi": 6, "gin": 3, "gorilla": 4, "echo": 2}
	for fw, n := range wantFw {
		if cen.ByFramework[fw] != n {
			t.Errorf("census %s = %d, want %d", fw, cen.ByFramework[fw], n)
		}
	}
	for _, r := range routes {
		name := frameworks.ContractName(r.Method, r.Path)
		if string(r.IID) != string(hash.ContractIID(name)) {
			t.Errorf("%s: iid is not ContractIID(%q)", r.Display, name)
		}
		if r.Path == "/chi/v1/orders/{}" && r.Method == "GET" && r.Display != "/chi/v1/orders/{orderID:[0-9]+}" {
			t.Errorf("display %q, want the pattern as written", r.Display)
		}
	}
}

// The resolver's edge cases, and the two shapes that must not become routes.
func TestRouteResolverEdgeCases(t *testing.T) {
	routes, cen := extract(t, "testdata/routes")
	const p = "example.com/routes."
	want := []string{
		"* /healthz net/http [1] " + p + "health",                 // http.HandleFunc: DefaultServeMux
		"GET /logged net/http [1] " + p + "health2",               // middleware: the wrapped handler
		"GET /made net/http [1] " + p + "makeHandler$1",           // factory: the closure it returns
		"* /items/{*} net/http [1] (" + p + "itemsAPI).ServeHTTP", // an http.Handler value, on a subtree pattern
		"GET /pets/{} net/http [1] " + p + "pets",                 // dynamic prefix dropped, suffix kept
		"POST /api/v2/things net/http [1] " + p + "things",        // StripPrefix mount through middleware
		"GET /field net/http [1] " + p + "health2",                // a router kept in a struct field
		"GET /known net/http [1] " + p + "health",                 // unknown prefix: kept as a suffix
	}
	diffRows(t, rows(routes), want)
	// unresolved: Register's handler parameter; prefixes: the dynamic one,
	// the all-hole path (no route), and Register's never-called mux
	if cen.HandlersUnresolved != 1 || cen.PrefixesUnresolved != 3 {
		t.Errorf("census = %+v, want handlers_unresolved=1 prefixes_unresolved=3", cen)
	}
	for _, r := range routes {
		if strings.Contains(r.Path, "unrooted") {
			t.Errorf("a parameter handler must not become a route: %+v", r)
		}
	}
}

// ---------------------------------------------------------------------------
// review fixes (coverage wave 1 review): testdata/review ports the
// reviewer's probes; one extraction serves every test below.
// ---------------------------------------------------------------------------

var reviewOnce struct {
	sync.Once
	routes []Route
	cen    Census
}

func reviewRoutes(t *testing.T) ([]Route, Census) {
	t.Helper()
	reviewOnce.Do(func() { reviewOnce.routes, reviewOnce.cen = extract(t, "testdata/review") })
	return reviewOnce.routes, reviewOnce.cen
}

// route finds the route of method+path; nil when absent.
func route(rs []Route, method, path string) *Route {
	for i := range rs {
		if rs[i].Method == method && rs[i].Path == path {
			return &rs[i]
		}
	}
	return nil
}

func wantRoute(t *testing.T, rs []Route, method, path, handler string, params []uint32) {
	t.Helper()
	r := route(rs, method, path)
	if r == nil {
		t.Errorf("missing %s %s; have %v", method, path, rows(rs))
		return
	}
	if r.Handler.String() != handler || fmt.Sprint(r.RequestParams) != fmt.Sprint(params) {
		t.Errorf("%s %s: handler %s params %v, want %s %v", method, path, r.Handler, r.RequestParams, handler, params)
	}
}

// Item 1: a func-typed argument that is not handler-shaped is a factory's
// dependency; the closure the factory returns is the handler. A wrapper
// whose argument IS a handler still resolves to that argument.
func TestFactoryDependencyIsNotTheHandler(t *testing.T) {
	rs, _ := reviewRoutes(t)
	wantRoute(t, rs, "GET", "/p3", "example.com/review/a.makeHandler$1", []uint32{1})
	wantRoute(t, rs, "GET", "/logged", "example.com/review/a.plain", []uint32{1})
	for _, r := range rs {
		if strings.HasSuffix(r.Handler.String(), "encodeJSON") {
			t.Errorf("%s %s bound to the dependency encodeJSON", r.Method, r.Path)
		}
	}
}

// Item 3: StripPrefix strips from the URL path, so a group or PathPrefix the
// parent routed through is not added again; chi Mount keeps adding it.
func TestStripPrefixUnderGroup(t *testing.T) {
	rs, _ := reviewRoutes(t)
	wantRoute(t, rs, "*", "/api/x", "example.com/review/b.plain", []uint32{1})      // gorilla PathPrefix("/api") + StripPrefix("/api")
	wantRoute(t, rs, "*", "/v1/files/x", "example.com/review/b.plain", []uint32{1}) // chi Route("/v1") + StripPrefix("/v1/files")
	wantRoute(t, rs, "*", "/api/y", "example.com/review/a.plain", []uint32{1})      // net/http "/api/v2/" + StripPrefix("/api")
	for _, bad := range []string{"/api/api/x", "/v1/v1/files/x"} {
		if route(rs, "*", bad) != nil {
			t.Errorf("doubled prefix %s", bad)
		}
	}
}

// Item 5: an unknown prefix piece after a literal is its own segment.
func TestDynamicMiddlePrefixKeepsSegments(t *testing.T) {
	rs, _ := reviewRoutes(t)
	wantRoute(t, rs, "GET", "/api/{}/mid", "example.com/review/a.plain", []uint32{1})
	if r := route(rs, "GET", "/api/{}/mid"); r != nil && r.Display != "/api/{}/mid" {
		t.Errorf("display %q", r.Display)
	}
}

// Item 7: gin joins a group and a relative path like path.Join.
func TestGinRelativePathJoin(t *testing.T) {
	rs, _ := reviewRoutes(t)
	wantRoute(t, rs, "GET", "/v1/users", "example.com/review/a.ginh", []uint32{0})
	if route(rs, "GET", "/v1users") != nil {
		t.Error("gin relative path concatenated without a separator")
	}
}

// Item 8: gin's Use returns the same group, so the chain keeps its prefix.
func TestGinUseKeepsGroup(t *testing.T) {
	rs, _ := reviewRoutes(t)
	wantRoute(t, rs, "GET", "/v1/used", "example.com/review/a.ginh", []uint32{0})
}

// Item 9: a register function reached by more prefixes than the analysis
// keeps still emits its route, as a suffix under an unknown prefix.
func TestPrefixOverflowKeepsSuffix(t *testing.T) {
	rs, cen := reviewRoutes(t)
	known := 0
	for i := 1; i <= 9; i++ {
		if route(rs, "GET", fmt.Sprintf("/p%d/shared", i)) != nil {
			known++
		}
	}
	if known != maxFacts {
		t.Errorf("%d prefixed routes, want the %d the cap keeps", known, maxFacts)
	}
	wantRoute(t, rs, "GET", "/shared", "example.com/review/b.plain2", []uint32{1})
	if cen.PrefixesUnresolved != 1 {
		t.Errorf("prefixes_unresolved = %d, want 1 (the overflowed registration)", cen.PrefixesUnresolved)
	}
}

// Item 11: a net/http subtree pattern serves everything below it; a host is
// dropped; `{$}` is exact.
func TestNetHTTPSubtreePatterns(t *testing.T) {
	rs, _ := reviewRoutes(t)
	wantRoute(t, rs, "*", "/static/{*}", "example.com/review/a.plain", []uint32{1})
	wantRoute(t, rs, "*", "/{*}", "example.com/review/a.plain", []uint32{1})
	wantRoute(t, rs, "GET", "/x", "example.com/review/a.plain", []uint32{1})
	if route(rs, "*", "/static") != nil {
		t.Error("/static/ must not be the literal path /static")
	}
}
