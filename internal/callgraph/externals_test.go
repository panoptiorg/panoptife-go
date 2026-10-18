package callgraph

import (
	"strings"
	"testing"

	xcg "golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/ssa"

	pb "github.com/panoptiorg/panoptife-go/internal/cgstorepb"
	"github.com/panoptiorg/panoptife-go/internal/hash"
	"github.com/panoptiorg/panoptife-go/internal/loader"
)

const xmodApp = "../../fixtures/xmod/app"

// loadXmod loads the app module of the xmod fixture, which calls into lib.
func loadXmod(t *testing.T) (*loader.Loaded, *xcg.Graph, hash.Owner) {
	t.Helper()
	ld, err := loader.Load(xmodApp, "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cg, _ := loader.BuildCallGraph(ld.Prog, "vta")
	if cg == nil {
		t.Fatal("vta graph build failed")
	}
	return ld, cg, hash.Owner{Repo: ld.Module, Modules: ld.Modules}
}

// mockFile is emit's file exclusion, reduced to what the fixture needs.
func mockFile(fn *ssa.Function) bool {
	return strings.HasSuffix(fn.Prog.Fset.Position(fn.Pos()).Filename, "_mock.go")
}

func newExt(ld *loader.Loaded, owner hash.Owner, depImpls bool) *Externals {
	return NewExternals(ld.Prog, owner, ld.DepPackages(), mockFile, depImpls)
}

func TestDepPackagesLeaveOutMocksAndScope(t *testing.T) {
	ld, _, _ := loadXmod(t)
	var paths []string
	for _, sp := range ld.DepPackages() {
		paths = append(paths, sp.Pkg.Path())
	}
	if got := strings.Join(paths, ","); got != "example.com/xmod/lib,example.com/xmod/lib/pb" {
		t.Errorf("dependency packages = %s; want lib and lib/pb (the mocks package, the app package and the standard library stay out)", got)
	}
}

// A promotion wrapper, which the call graph names as the target, maps to the
// method it wraps. Nothing else outside the emitted set maps to anything.
func TestDeclaredNormalizesWrapper(t *testing.T) {
	ld, cg, owner := loadXmod(t)
	ext := newExt(ld, owner, true)
	site := findInvoke(t, ld.Prog, "tagHandler", "Tag")

	var raw []*ssa.Function
	for _, e := range cg.Nodes[site.Parent()].Out {
		if e.Site == site {
			raw = append(raw, e.Callee.Func)
		}
	}
	if len(raw) != 1 || raw[0].Synthetic == "" {
		t.Fatalf("fixture drift: want one synthetic call-graph target, got %v", fqns(raw))
	}
	if ext.Is(raw[0]) {
		t.Errorf("a wrapper is not a declared function: Is(%s) = true", raw[0])
	}
	got := ext.Declared(raw[0])
	if got == nil || got.String() != "(example.com/xmod/lib.Base).Tag" || got.Synthetic != "" {
		t.Fatalf("Declared(%s) = %v, want the declared (lib.Base).Tag", raw[0], got)
	}
	if ext.Declared(got) != got {
		t.Error("a declared function must map to itself")
	}
	for _, fn := range []*ssa.Function{
		ld.Prog.ImportedPackage("strings").Func("ToUpper"),           // standard library
		ld.Prog.ImportedPackage("example.com/xmod/app").Func("user"), // in scope
		nil,
	} {
		if d := ext.Declared(fn); d != nil {
			t.Errorf("Declared(%v) = %v, want nil", fn, d)
		}
	}
	if (*Externals)(nil).Declared(raw[0]) != nil || (*Externals)(nil).Is(got) {
		t.Error("a nil Externals knows no external function")
	}
}

func TestResolverTargetsAcrossModules(t *testing.T) {
	ld, cg, owner := loadXmod(t)
	ext := newExt(ld, owner, true)
	r := NewResolver(cg, DefaultFanoutCap, nil).WithExternals(ext)

	for _, c := range []struct {
		name, fn, method string
		want             []string
	}{
		// the concrete lib type flows into an app interface: the call graph's own edge
		{"concrete lib type", "saveHandler", "Save", []string{"(*example.com/xmod/lib.store).Save"}},
		// lib's constructor returns an interface: the call graph has no edge, the declared implementations answer
		{"constructor returns an interface", "doHandler", "Do", []string{"(*example.com/xmod/lib.client).Do"}},
		{"interface that lib declares", "runHandler", "Run", []string{"(*example.com/xmod/lib.shell).Run"}},
		{"mixed in-scope and external", "mixHandler", "Mix", []string{"(*example.com/xmod/lib.Mixer).Mix", "(example.com/xmod/app.localMix).Mix"}},
		{"standard library only", "writeHandler", "Write", nil},
	} {
		site := findInvoke(t, ld.Prog, c.fn, c.method)
		got, conf, capped := r.TargetsAt(site)
		if capped || strings.Join(fqns(got), "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: targets %v capped=%v, want %v", c.name, fqns(got), capped, c.want)
		}
		if n := len(c.want); n > 1 && conf != 1/float32(n) {
			t.Errorf("%s: confidence %v, want 1/%d", c.name, conf, n)
		}
	}
	for _, c := range []struct {
		fn, method string
		want       bool
	}{{"doHandler", "Do", true}, {"runHandler", "Run", true}, {"saveHandler", "Save", false}, {"mixHandler", "Mix", false}, {"writeHandler", "Write", false}} {
		if got := r.DepImplSite(findInvoke(t, ld.Prog, c.fn, c.method)); got != c.want {
			t.Errorf("DepImplSite(%s) = %v, want %v", c.fn, got, c.want)
		}
	}
}

// No dependency target and no declared implementation without Externals, or
// with the implementations rule off; the call graph's own dependency targets
// stay in the second case.
func TestResolverWithoutExternals(t *testing.T) {
	ld, cg, owner := loadXmod(t)
	none := NewResolver(cg, DefaultFanoutCap, nil)
	noImpls := NewResolver(cg, DefaultFanoutCap, nil).WithExternals(newExt(ld, owner, false))

	for _, c := range []struct {
		fn, method         string
		plain, noImplsWant []string
	}{
		{"saveHandler", "Save", nil, []string{"(*example.com/xmod/lib.store).Save"}},
		{"doHandler", "Do", nil, nil},
		{"runHandler", "Run", nil, nil},
		{"mixHandler", "Mix", []string{"(example.com/xmod/app.localMix).Mix"}, []string{"(*example.com/xmod/lib.Mixer).Mix", "(example.com/xmod/app.localMix).Mix"}},
	} {
		site := findInvoke(t, ld.Prog, c.fn, c.method)
		if got, _, capped := none.TargetsAt(site); capped || strings.Join(fqns(got), "|") != strings.Join(c.plain, "|") {
			t.Errorf("%s no externals: %v capped=%v, want %v", c.fn, fqns(got), capped, c.plain)
		}
		if got, _, capped := noImpls.TargetsAt(site); capped || strings.Join(fqns(got), "|") != strings.Join(c.noImplsWant, "|") {
			t.Errorf("%s implementations off: %v capped=%v, want %v", c.fn, fqns(got), capped, c.noImplsWant)
		}
	}
	if noImpls.DepImplSite(findInvoke(t, ld.Prog, "doHandler", "Do")) {
		t.Error("DepImplSite is true with the rule off")
	}
}

// The cap decides, and never makes a site opaque that was not opaque before.
func TestDependencyTargetsRespectCap(t *testing.T) {
	ld, cg, owner := loadXmod(t)
	ext := newExt(ld, owner, true)

	// Eleven declared implementations: over the default cap, so the site keeps
	// its previous answer (no target, not capped); under a larger cap it lists all.
	label := findInvoke(t, ld.Prog, "labelHandler", "Label")
	if got, _, capped := NewResolver(cg, DefaultFanoutCap, nil).WithExternals(ext).TargetsAt(label); capped || len(got) != 0 {
		t.Errorf("11 implementations, cap %d: %d targets capped=%v, want none and not capped", DefaultFanoutCap, len(got), capped)
	}
	got, conf, capped := NewResolver(cg, 11, nil).WithExternals(ext).TargetsAt(label)
	if capped || len(got) != 11 || conf != 1.0/11 {
		t.Errorf("11 implementations, cap 11: %d targets conf=%v capped=%v, want 11, 1/11, false", len(got), conf, capped)
	}
	for i := 1; i < len(got); i++ {
		if hash.FQN(got[i-1]) >= hash.FQN(got[i]) {
			t.Errorf("targets out of order: %s before %s", hash.FQN(got[i-1]), hash.FQN(got[i]))
		}
	}

	// One in-scope and one external target: both fit under cap 2; under cap 1
	// the external target would exceed it, so the site keeps the in-scope one.
	mix := findInvoke(t, ld.Prog, "mixHandler", "Mix")
	if got, _, capped := NewResolver(cg, 2, nil).WithExternals(ext).TargetsAt(mix); capped || len(got) != 2 {
		t.Errorf("mix, cap 2: %v capped=%v, want both targets", fqns(got), capped)
	}
	got, conf, capped = NewResolver(cg, 1, nil).WithExternals(ext).TargetsAt(mix)
	if capped || len(got) != 1 || got[0].String() != "(example.com/xmod/app.localMix).Mix" || conf != 1.0 {
		t.Errorf("mix, cap 1: %v conf=%v capped=%v, want only the in-scope target", fqns(got), conf, capped)
	}

	// Test doubles are no implementation: lib has two (a mockgen-style file and
	// a mocks package). Without the file exclusion the site lists the double too.
	do := findInvoke(t, ld.Prog, "doHandler", "Do")
	withDouble := NewExternals(ld.Prog, owner, ld.DepPackages(), nil, true)
	if got, _, _ := NewResolver(cg, DefaultFanoutCap, nil).WithExternals(withDouble).TargetsAt(do); len(got) != 2 {
		t.Errorf("no file exclusion: %v, want the client and the double in client_mock.go", fqns(got))
	}
	if got, _, _ := NewResolver(cg, DefaultFanoutCap, nil).WithExternals(ext).TargetsAt(do); len(got) != 1 {
		t.Errorf("file exclusion: %v, want the client only", fqns(got))
	}
}

// Embedding: lib.Wrapper has Tag only by promotion. Every route to the method
// names the one declared method, and no wrapper.
func TestDeclaredImplementationsUseDeclaredMethod(t *testing.T) {
	ld, cg, owner := loadXmod(t)
	r := NewResolver(cg, DefaultFanoutCap, nil).WithExternals(newExt(ld, owner, true))
	site := findInvoke(t, ld.Prog, "tagHandler", "Tag")
	got, _, _ := r.TargetsAt(site)
	if len(got) != 1 || got[0].String() != "(example.com/xmod/lib.Base).Tag" || got[0].Synthetic != "" {
		t.Errorf("targets %v, want the declared (lib.Base).Tag", fqns(got))
	}
}

// A snapshot whose callee iid names a function of a dependency module replays
// through Externals; without it the whole snapshot is refused.
func TestSnapshotReplaysExternalTargets(t *testing.T) {
	ld, cg, owner := loadXmod(t)
	ext := newExt(ld, owner, true)
	site := findInvoke(t, ld.Prog, "doHandler", "Do")
	fn := site.Parent()
	targets, conf, _ := NewResolver(cg, DefaultFanoutCap, nil).WithExternals(ext).TargetsAt(site)
	if len(targets) != 1 {
		t.Fatalf("fixture drift: %v", fqns(targets))
	}
	callerIID := owner.IID(fn)
	calls := callInstructions(fn, nil)
	ord := -1
	for i, c := range calls {
		if c == site {
			ord = i
		}
	}
	meta := &pb.Meta{}
	shards := map[string]*pb.PkgShard{"p": {Pkg: "p", Callers: []*pb.CallerEdges{{
		CallerIid:  callerIID,
		NCallsites: uint32(len(calls)),
		Sites:      []*pb.SiteEdge{{CallsiteId: uint32(ord), CalleeIids: [][]byte{owner.IID(targets[0])}, Confidence: conf}},
	}}}}
	emitted := map[string]*ssa.Function{string(callerIID): fn}

	if _, err := NewSnapshotResolver(meta, shards, emitted, nil, nil); err == nil {
		t.Error("an external callee iid without Externals must reject the snapshot")
	}
	sr, err := NewSnapshotResolver(meta, shards, emitted, nil, ext)
	if err != nil {
		t.Fatalf("with Externals: %v", err)
	}
	if got, _, capped := sr.TargetsAt(site); capped || len(got) != 1 || got[0] != targets[0] {
		t.Errorf("replay = %v capped=%v, want %v", fqns(got), capped, fqns(targets))
	}

	// An iid that no dependency declares still rejects it.
	shards["p"].Callers[0].Sites[0].CalleeIids = [][]byte{[]byte("bogus-bogus-bogus-bogus-bogus-32")}
	if _, err := NewSnapshotResolver(meta, shards, emitted, nil, ext); err == nil {
		t.Error("an unknown callee iid must still reject the snapshot")
	}
}

// An interface of the standard library gets no declared implementations, even
// though lib has a type that fits it. An interface of the app does.
func TestStdlibInterfaceHasNoDeclaredImplementations(t *testing.T) {
	ld, cg, owner := loadXmod(t)
	r := NewResolver(cg, DefaultFanoutCap, nil).WithExternals(newExt(ld, owner, true))

	site := findInvoke(t, ld.Prog, "logTo", "Write")
	if got, _, capped := r.TargetsAt(site); capped || len(got) != 0 {
		t.Errorf("io.Writer: targets %v capped=%v, want none", fqns(got), capped)
	}
	if r.DepImplSite(site) {
		t.Error("io.Writer: the site is reported as resolved by declared implementations")
	}
	// the same method set through the app's own interface
	if got, _, _ := r.TargetsAt(findInvoke(t, ld.Prog, "doHandler", "Do")); len(got) != 1 {
		t.Errorf("app interface: %v, want the one declared implementation", fqns(got))
	}
}

// A method expression is a wrapper that takes the receiver as its first
// parameter. Naming the declared method for it would shift the arguments.
func TestDeclaredDropsMethodExpressionThunk(t *testing.T) {
	ld, cg, owner := loadXmod(t)
	ext := newExt(ld, owner, true)
	var thunk, bound *ssa.Function
	for _, e := range cg.Nodes[ld.Prog.ImportedPackage("example.com/xmod/app").Func("applyT")].Out {
		if e.Site != nil {
			thunk = e.Callee.Func
		}
	}
	for _, e := range cg.Nodes[ld.Prog.ImportedPackage("example.com/xmod/app").Func("applyB")].Out {
		if e.Site != nil {
			bound = e.Callee.Func
		}
	}
	if thunk == nil || thunk.Synthetic == "" || bound == nil || bound.Synthetic == "" {
		t.Fatalf("fixture drift: thunk=%v bound=%v", thunk, bound)
	}
	if d := ext.Declared(thunk); d != nil {
		t.Errorf("Declared(%s) = %s, want nil", thunk, d)
	}
	if d := ext.Declared(bound); d == nil || d.String() != "(example.com/xmod/lib.Base).Tag" {
		t.Errorf("Declared(%s) = %v, want the declared method", bound, d)
	}
}
