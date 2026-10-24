package emit

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	pb "github.com/panoptiorg/panoptife-go/internal/cgfpb"
	"github.com/panoptiorg/panoptife-go/internal/hash"
)

// Interface calls into another module. The app calls lib through interfaces;
// where the call graph cannot say which type is behind one, dispatch names the
// methods that lib declares, and each target carries the iid that lib's own
// CGF has.

// virtualSites returns the VIRTUAL sites of function fqn, by callee fqn.
func virtualSites(t *testing.T, pkgs map[string]*pb.CgfPackage, fqn string) map[string]*pb.CallSite {
	t.Helper()
	f := fnByFqn(pkgs, fqn)
	if f == nil {
		t.Fatalf("function %s not emitted", fqn)
	}
	out := map[string]*pb.CallSite{}
	for _, cs := range f.Flow.Callsites {
		if cs.Kind == pb.CallSite_VIRTUAL {
			out[cs.CalleeFqn] = cs
		}
	}
	return out
}

// iidsByFqn maps the fqn of every emitted function to its iid.
func iidsByFqn(pkgs map[string]*pb.CgfPackage) map[string][]byte {
	m := map[string][]byte{}
	for _, cp := range pkgs {
		for _, f := range cp.Functions {
			m[f.Fqn] = f.Id.Iid
		}
	}
	return m
}

const (
	pkgApp = "example.com/xmod/app"
	pkgLib = "example.com/xmod/lib"
)

// wantSite is the expected dispatch of one interface call in the app.
type wantSite struct {
	fn, callee string
	targets    []string // target_fqns; nil: none, and the site keeps the placeholder iid
}

var xmodSites = []wantSite{
	// a lib constructor returns lib's interface; the app narrows it
	{"app.doHandler", "(" + pkgApp + ".doer).Do", []string{"(*" + pkgLib + ".client).Do"}},
	// a lib constructor returns an unexported concrete type; the app assigns it to its own interface
	{"app.saveHandler", "(" + pkgApp + ".putter).Save", []string{"(*" + pkgLib + ".store).Save"}},
	// a method of lib's own exported interface
	{"app.runHandler", "(" + pkgLib + ".Runner).Run", []string{"(*" + pkgLib + ".shell).Run"}},
	// value-receiver method reached through a pointer, and a promoted method:
	// both name the declared method
	{"app.tagHandler", "(" + pkgApp + ".tagger).Tag", []string{"(" + pkgLib + ".Base).Tag"}},
	{"app.tagHandler", "(" + pkgApp + ".tagger2).Tag", []string{"(" + pkgLib + ".Base).Tag"}},
	// an app type and a lib type both reach the site
	{"app.mixHandler", "(" + pkgApp + ".mixer).Mix", []string{"(*" + pkgLib + ".Mixer).Mix", "(" + pkgApp + ".localMix).Mix"}},
	// two implementations in lib, one of which the constructor returns: the
	// declared implementations list both. --dep-bodies narrows it to one.
	{"app.cacheHandler", "(" + pkgApp + ".getter).Get", []string{"(*" + pkgLib + ".diskCache).Get", "(*" + pkgLib + ".memCache).Get"}},
	// the one implementation in lib ignores its argument
	{"app.echoHandler", "(" + pkgApp + ".echoer).Echo", []string{"(" + pkgLib + ".plainEcho).Echo"}},
	// eleven implementations: more than a site lists, so nothing changes
	{"app.labelHandler", "(" + pkgApp + ".labeled).Label", nil},
	// io.Writer is the standard library's: lib.Counter implements it, and is
	// not named, whether or not the call graph saw the writer's type
	{"app.writeHandler", "(io.Writer).Write", nil},
	{"app.logTo", "(io.Writer).Write", nil},
}

func checkXmodSites(t *testing.T, app, lib map[string]*pb.CgfPackage, sites []wantSite) {
	t.Helper()
	// Every target is a function of lib or of the app, and the module that
	// declares it has its own iid for it.
	own := iidsByFqn(lib)
	for fqn, iid := range iidsByFqn(app) {
		own[fqn] = iid
	}
	for _, w := range sites {
		cs := virtualSites(t, app, pkgApp+strings.TrimPrefix(w.fn, "app"))[w.callee]
		if cs == nil {
			t.Errorf("%s: no site calling %s", w.fn, w.callee)
			continue
		}
		if len(w.targets) == 0 {
			// the result the site had before: one placeholder iid made of the
			// interface method's name, and no target names
			placeholder := hash.IIDFromParts("", "", w.callee, "")
			if len(cs.TargetFqns) != 0 || len(cs.CalleeIids) != 1 || !bytes.Equal(cs.CalleeIids[0], placeholder) || cs.Opaque {
				t.Errorf("%s: %s: target_fqns=%v callee_iids=%d opaque=%v, want the placeholder iid alone", w.fn, w.callee, cs.TargetFqns, len(cs.CalleeIids), cs.Opaque)
			}
			continue
		}
		// The targets are followed by the entry the site has without any
		// target, so the core keeps its default leaf next to the summaries.
		wantFqns := append(append([]string{}, w.targets...), w.callee)
		if strings.Join(cs.TargetFqns, "|") != strings.Join(wantFqns, "|") {
			t.Errorf("%s: %s: target_fqns=%v, want %v", w.fn, w.callee, cs.TargetFqns, wantFqns)
		}
		if len(cs.CalleeIids) != len(cs.TargetFqns) {
			t.Errorf("%s: %s: %d callee iids for %d target names", w.fn, w.callee, len(cs.CalleeIids), len(cs.TargetFqns))
			continue
		}
		last := len(cs.CalleeIids) - 1
		if !bytes.Equal(cs.CalleeIids[last], hash.IIDFromParts("", "", w.callee, "")) {
			t.Errorf("%s: %s: the last callee iid is not the unresolved entry", w.fn, w.callee)
		}
		if want := 1 / float32(len(cs.CalleeIids)); cs.DispatchConfidence != want {
			t.Errorf("%s: %s: dispatch_confidence %v, want %v", w.fn, w.callee, cs.DispatchConfidence, want)
		}
		for i, fqn := range cs.TargetFqns[:last] {
			want := own[fqn]
			if want == nil || !bytes.Equal(cs.CalleeIids[i], want) {
				t.Errorf("%s: target %s: callee iid %x, the declaring module's own iid is %x", w.fn, fqn, cs.CalleeIids[i], want)
			}
		}
	}
}

func TestDependencyDispatch(t *testing.T) {
	lib, _ := runCGF(t, Options{RepoDir: "../../fixtures/xmod/lib"})
	app, stderr := runCGF(t, Options{RepoDir: "../../fixtures/xmod/app"})
	checkXmodSites(t, app, lib, xmodSites)

	// Test doubles are never a target.
	for _, cp := range app {
		for _, f := range cp.Functions {
			for _, cs := range f.Flow.Callsites {
				for _, fqn := range cs.TargetFqns {
					if strings.Contains(fqn, "clientMock") || strings.Contains(fqn, "mocks.") {
						t.Errorf("%s: a test double is a target: %s", f.Fqn, fqn)
					}
				}
			}
		}
	}
	// target_fqns is not part of the body identity: the same body with other
	// target names has the same flow hash input.
	for _, cp := range app {
		for _, f := range cp.Functions {
			for _, cs := range f.Flow.Callsites {
				if len(cs.TargetFqns) > 0 {
					c := proto.Clone(f.Flow).(*pb.LocalFlow)
					for _, s := range c.Callsites {
						s.TargetFqns = []string{"other"}
					}
					if !bytes.Equal(canonicalFlow(f.Flow), canonicalFlow(c)) {
						t.Errorf("%s: target_fqns changes the canonical flow", f.Fqn)
					}
					break
				}
			}
		}
	}
	// 10 sites: doHandler, saveHandler, runHandler, both tagHandler calls,
	// mixHandler, cacheHandler, echoHandler and two calls through a function
	// value (applyF, applyB); 5 of them have no target but the declared
	// implementations.
	if l := line(t, stderr, "opaque:"); !strings.HasSuffix(l, "external=10 dep-impls=5") {
		t.Errorf("census line %q, want it to end in external=10 dep-impls=5", l)
	}
	if strings.Contains(stderr, "dangling callee iid") {
		t.Errorf("a dependency target was reported dangling:\n%s", stderr)
	}
	// Schema 3: target_fqns exists.
	for _, cp := range app {
		if cp.SchemaVersion != 3 {
			t.Errorf("%s: schema_version %d, want 3", cp.PackagePath, cp.SchemaVersion)
		}
	}
}

// Without the declared-implementations rule, an interface call keeps no target
// when the call graph finds none. Targets the call graph itself finds in lib
// stay.
func TestNoDepImpls(t *testing.T) {
	lib, _ := runCGF(t, Options{RepoDir: "../../fixtures/xmod/lib"})
	app, stderr := runCGF(t, Options{RepoDir: "../../fixtures/xmod/app", NoDepImpls: true})
	var want []wantSite
	for _, w := range xmodSites {
		switch w.fn {
		case "app.doHandler", "app.runHandler", "app.cacheHandler", "app.echoHandler":
			w.targets = nil
		case "app.tagHandler":
			if strings.Contains(w.callee, "tagger2") {
				w.targets = nil
			}
		}
		want = append(want, w)
	}
	checkXmodSites(t, app, lib, want)
	if l := line(t, stderr, "opaque:"); !strings.HasSuffix(l, "external=5 dep-impls=0") {
		t.Errorf("census line %q, want it to end in external=5 dep-impls=0", l)
	}
}

// functionSet is the sorted list of function iids of every package.
func functionSet(pkgs map[string]*pb.CgfPackage) []string {
	var out []string
	for _, cp := range pkgs {
		for _, f := range cp.Functions {
			out = append(out, string(f.Id.Iid))
		}
	}
	sort.Strings(out)
	return out
}

// Building the bodies of dependencies lets the call graph see more. The xmod
// sites keep their targets, except that a site whose declared implementations
// are two now lists the one that flows to it.
func TestDepBodies(t *testing.T) {
	lib, _ := runCGF(t, Options{RepoDir: "../../fixtures/xmod/lib"})
	app, stderr := runCGF(t, Options{RepoDir: "../../fixtures/xmod/app", DepBodies: true})
	var want []wantSite
	for _, w := range xmodSites {
		if w.fn == "app.cacheHandler" {
			w.targets = []string{"(*" + pkgLib + ".memCache).Get"}
		}
		want = append(want, w)
	}
	checkXmodSites(t, app, lib, want)
	if !strings.Contains(line(t, stderr, "pc-fe timing:"), "dep-bodies=") {
		t.Errorf("the timing line must show the dependency build:\n%s", stderr)
	}
	// With the flag off the timing line has no such field.
	_, plain := runCGF(t, Options{RepoDir: "../../fixtures/xmod/app"})
	if strings.Contains(line(t, plain, "pc-fe timing:"), "dep-bodies=") {
		t.Error("dep-bodies shown without the flag")
	}
}

// --dep-bodies never changes which functions are emitted, on any fixture.
func TestDepBodiesKeepsEmittedFunctions(t *testing.T) {
	var dirs []string
	for _, pat := range []string{"../../fixtures/*/go.mod", "../../fixtures/*/*/go.mod"} {
		ms, _ := filepath.Glob(pat)
		for _, m := range ms {
			dirs = append(dirs, filepath.Dir(m))
		}
	}
	if len(dirs) < 20 {
		t.Fatalf("found %d fixture modules, want at least 20", len(dirs))
	}
	for _, dir := range dirs {
		t.Run(strings.TrimPrefix(dir, "../../fixtures/"), func(t *testing.T) {
			plain, _ := runCGF(t, Options{RepoDir: dir})
			bodies, _ := runCGF(t, Options{RepoDir: dir, DepBodies: true})
			a, b := functionSet(plain), functionSet(bodies)
			if len(a) == 0 || strings.Join(a, "") != strings.Join(b, "") {
				t.Errorf("emitted functions differ: %d without --dep-bodies, %d with", len(a), len(b))
			}
		})
	}
}

// The cgstore keeps a site's dependency targets: a replay rebuilds no call
// graph, finds the targets again through the declared functions of the
// dependency packages, and gives the same CGF bytes. Each flag has its own
// store namespace.
// xmodRepo copies the xmod fixture into a clean git repository and returns the
// app module's directory.
func xmodRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := filepath.Join(t.TempDir(), "xmod")
	if err := os.CopyFS(root, os.DirFS("../../fixtures/xmod")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "."},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "fixture"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return filepath.Join(root, "app")
}

func TestCgstoreReplayExternalTargets(t *testing.T) {
	app := xmodRepo(t)
	store := t.TempDir()
	o := Options{RepoDir: app, CgStoreDir: store}
	o.OutDir = t.TempDir()
	livePkgs, live := runCGF(t, o)
	if !strings.Contains(live, "dispatch[vta]") {
		t.Fatalf("first run should build the graph live:\n%s", live)
	}
	o.OutDir = t.TempDir()
	replayPkgs, replay := runCGF(t, o)
	if !strings.Contains(replay, "dispatch[vta-cached]") || strings.Contains(replay, "rebuilding") {
		t.Fatalf("second run must replay the snapshot:\n%s", replay)
	}
	if l := line(t, replay, "opaque:"); !strings.Contains(l, "external=10") || strings.Contains(l, "dep-impls") {
		t.Errorf("replayed census %q: want external=10 and no dep-impls", l)
	}
	if len(livePkgs) == 0 || len(livePkgs) != len(replayPkgs) {
		t.Fatalf("package count %d vs %d", len(livePkgs), len(replayPkgs))
	}
	for path, cp := range livePkgs {
		x, _ := proto.MarshalOptions{Deterministic: true}.Marshal(cp)
		y, _ := proto.MarshalOptions{Deterministic: true}.Marshal(replayPkgs[path])
		if !bytes.Equal(x, y) {
			t.Errorf("%s: replayed CGF differs from the live one", path)
		}
	}
	// A run with another flag setting does not meet this snapshot.
	o.OutDir = t.TempDir()
	o.NoDepImpls = true
	if _, other := runCGF(t, o); !strings.Contains(other, "dispatch[vta]") || strings.Contains(other, "cached") {
		t.Errorf("--no-dep-impls must not replay the default snapshot:\n%s", other)
	}
	o.NoDepImpls = false
	o.DepBodies = true
	o.OutDir = t.TempDir()
	if _, other := runCGF(t, o); strings.Contains(other, "cached") {
		t.Errorf("--dep-bodies must not replay the default snapshot:\n%s", other)
	}
}

// A site whose targets are all emitted functions is unchanged: no unresolved
// entry, no name beyond the targets'.
func TestInScopeTargetsKeepNoUnresolvedEntry(t *testing.T) {
	pkgs, _ := runCGF(t, Options{RepoDir: "../../fixtures/dispatch"})
	f := fnByFqn(pkgs, "(*example.com/dispatch/app.App).HandleNarrow")
	if f == nil {
		t.Fatal("HandleNarrow not emitted")
	}
	found := false
	for _, cs := range f.Flow.Callsites {
		if cs.Kind != pb.CallSite_VIRTUAL || len(cs.TargetFqns) == 0 {
			continue
		}
		found = true
		if len(cs.CalleeIids) != 2 || len(cs.TargetFqns) != 2 || cs.DispatchConfidence != 0.5 {
			t.Errorf("%s: %d iids, %d names, confidence %v; want 2, 2, 0.5", cs.CalleeFqn, len(cs.CalleeIids), len(cs.TargetFqns), cs.DispatchConfidence)
		}
		for _, fqn := range cs.TargetFqns {
			if fqn == cs.CalleeFqn {
				t.Errorf("%s: unresolved entry on a site with in-scope targets only", cs.CalleeFqn)
			}
		}
	}
	if !found {
		t.Fatal("fixture drift: HandleNarrow has no dispatched site")
	}
}

// Calls through a function value: a declared function and a bound method are
// named like any other dispatch target, and keep the unresolved entry. A
// method expression is not: the call passes the receiver as its first
// argument, which the declared method does not take.
func TestFuncValueCallsIntoDependency(t *testing.T) {
	lib, _ := runCGF(t, Options{RepoDir: "../../fixtures/xmod/lib"})
	app, _ := runCGF(t, Options{RepoDir: "../../fixtures/xmod/app"})
	libIID := iidsByFqn(lib)
	only := func(fn string) *pb.CallSite {
		t.Helper()
		f := fnByFqn(app, pkgApp+"."+fn)
		if f == nil || len(f.Flow.Callsites) != 1 {
			t.Fatalf("fixture drift: %s", fn)
		}
		return f.Flow.Callsites[0]
	}
	for _, c := range []struct{ fn, target string }{
		{"applyF", pkgLib + ".Fetch"},
		{"applyB", "(" + pkgLib + ".Base).Tag"},
	} {
		cs := only(c.fn)
		unresolved := hash.IIDFromParts("", "", cs.CalleeFqn, "")
		if cs.Opaque || len(cs.CalleeIids) != 2 || len(cs.TargetFqns) != 2 ||
			cs.TargetFqns[0] != c.target || cs.TargetFqns[1] != cs.CalleeFqn ||
			!bytes.Equal(cs.CalleeIids[0], libIID[c.target]) || !bytes.Equal(cs.CalleeIids[1], unresolved) ||
			cs.DispatchConfidence != 0.5 {
			t.Errorf("%s: opaque=%v targets=%v iids=%d conf=%v; want %s then the unresolved entry, not opaque", c.fn, cs.Opaque, cs.TargetFqns, len(cs.CalleeIids), cs.DispatchConfidence, c.target)
		}
	}
	cs := only("applyT")
	if !cs.Opaque || len(cs.TargetFqns) != 0 || len(cs.CalleeIids) != 1 ||
		!bytes.Equal(cs.CalleeIids[0], hash.IIDFromParts("", "", cs.CalleeFqn, "")) {
		t.Errorf("applyT: opaque=%v targets=%v iids=%d; want the site as it is without targets (the method expression is dropped)", cs.Opaque, cs.TargetFqns, len(cs.CalleeIids))
	}
}

// census reads the key=N fields of the opaque: line.
func census(t *testing.T, stderr string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, f := range strings.Fields(line(t, stderr, "opaque:")) {
		if k, v, ok := strings.Cut(f, "="); ok {
			n := 0
			for _, c := range v {
				n = n*10 + int(c-'0')
			}
			out[k] = n
		}
	}
	return out
}

// With --dep-bodies the call graph is built over the functions collected
// before the dependency bodies, plus the dependencies' own. It must not fail
// quietly (it did, on a type parameter in a dependency body), and it must find
// at least what it finds without the flag.
func TestDepBodiesKeepsDispatchOnRealDependencies(t *testing.T) {
	plainPkgs, plain := runCGF(t, Options{RepoDir: "../../fixtures/httpapi"})
	bodyPkgs, bodies := runCGF(t, Options{RepoDir: "../../fixtures/httpapi", DepBodies: true})
	if strings.Contains(bodies, "dispatch-warn") || !strings.Contains(bodies, "dispatch[vta]") || strings.Contains(bodies, "FAILED") {
		t.Fatalf("the call graph failed with --dep-bodies:\n%s", bodies)
	}
	p, b := census(t, plain), census(t, bodies)
	if b["unresolved"] > p["unresolved"] || b["external"] < p["external"] || b["external"] == 0 {
		t.Errorf("--dep-bodies census %v is worse than %v", b, p)
	}
	// A known site: chi's Router is an interface of a module, and the router
	// the handler builds is a *chi.Mux.
	for name, pkgs := range map[string]map[string]*pb.CgfPackage{"plain": plainPkgs, "dep-bodies": bodyPkgs} {
		cs := virtualSites(t, pkgs, "example.com/httpapi/chiapi.registerOrders")["(github.com/go-chi/chi/v5.Router).Post"]
		if cs == nil || len(cs.TargetFqns) < 1 || cs.TargetFqns[0] != "(*github.com/go-chi/chi/v5.Mux).Post" {
			t.Errorf("%s: registerOrders Router.Post targets %v, want (*chi.Mux).Post first", name, cs.GetTargetFqns())
		}
	}
}

// Dependency bodies change the CGF beyond the call graph: a getter in a
// dependency package is a field read once its body is built, so its call site
// loses its arg ports (canonicalized to the zero-arg shell). A snapshot is
// replayed against the same build, so a replay under --dep-bodies hits the
// cache and gives the live bytes.
func TestCgstoreReplayWithDepBodies(t *testing.T) {
	app := xmodRepo(t)
	handlerArgc := func(pkgs map[string]*pb.CgfPackage) (total uint32, shells int) {
		f := fnByFqn(pkgs, pkgApp+".pbHandler")
		if f == nil {
			t.Fatal("pbHandler not emitted")
		}
		for _, cs := range f.Flow.Callsites {
			total += cs.Argc
			if cs.Argc == 0 {
				shells++
			}
		}
		return total, shells
	}
	plain, _ := runCGF(t, Options{RepoDir: app})
	o := Options{RepoDir: app, CgStoreDir: t.TempDir(), DepBodies: true}
	live, liveErr := runCGF(t, o)
	if !strings.Contains(liveErr, "dispatch[vta]") {
		t.Fatalf("first run should build the graph live:\n%s", liveErr)
	}
	plainArgc, _ := handlerArgc(plain)
	liveArgc, liveShells := handlerArgc(live)
	if liveArgc >= plainArgc || liveShells == 0 {
		t.Fatalf("fixture drift: the getter call site must lose its arg ports with the dependency body (%d argc, %d shells with; %d without)", liveArgc, liveShells, plainArgc)
	}
	replay, replayErr := runCGF(t, o)
	if !strings.Contains(replayErr, "dispatch[vta-cached]") || strings.Contains(replayErr, "rebuilding") {
		t.Fatalf("second run must replay the snapshot:\n%s", replayErr)
	}
	for path, cp := range live {
		x, _ := proto.MarshalOptions{Deterministic: true}.Marshal(cp)
		y, _ := proto.MarshalOptions{Deterministic: true}.Marshal(replay[path])
		if !bytes.Equal(x, y) {
			t.Errorf("%s: replayed CGF differs from the live one", path)
		}
	}
}
