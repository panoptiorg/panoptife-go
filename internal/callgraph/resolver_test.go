package callgraph

import (
	"strings"
	"testing"

	xcg "golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/vta"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/panoptiorg/panoptife-go/internal/loader"
)

const fixture = "../../fixtures/dispatch"

func loadFixture(t *testing.T) (*loader.Loaded, *xcg.Graph) {
	t.Helper()
	ld, err := loader.Load(fixture, "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cg, _ := loader.BuildCallGraph(ld.Prog, "vta")
	if cg == nil {
		t.Fatalf("vta graph build failed")
	}
	return ld, cg
}

// findInvoke locates the interface-dispatch call of methodName inside the
// function whose FQN contains fnPart.
func findInvoke(t *testing.T, prog *ssa.Program, fnPart, methodName string) ssa.CallInstruction {
	t.Helper()
	for f := range ssautil.AllFunctions(prog) {
		if !strings.Contains(f.String(), fnPart) || f.Blocks == nil {
			continue
		}
		for _, blk := range f.Blocks {
			for _, in := range blk.Instrs {
				call, ok := in.(ssa.CallInstruction)
				if !ok {
					continue
				}
				cc := call.Common()
				if cc.IsInvoke() && cc.Method.Name() == methodName {
					return call
				}
			}
		}
	}
	t.Fatalf("no invoke of %s in %s", methodName, fnPart)
	return nil
}

func fqns(targets []*ssa.Function) []string {
	var out []string
	for _, f := range targets {
		out = append(out, f.String())
	}
	return out
}

func TestNarrowResolvedSorted(t *testing.T) {
	ld, cg := loadFixture(t)
	r := NewResolver(cg, DefaultFanoutCap, nil)
	site := findInvoke(t, ld.Prog, "HandleNarrow", "Save")

	targets, conf, capped := r.TargetsAt(site)
	if capped {
		t.Fatalf("narrow site capped")
	}
	got := fqns(targets)
	want := []string{
		"(*example.com/dispatch/app.MemRepo).Save",
		"(*example.com/dispatch/app.PgRepo).Save",
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("targets = %v, want %v (sorted by FQN)", got, want)
	}
	if conf != 0.5 {
		t.Fatalf("confidence = %v, want 0.5", conf)
	}
}

func TestWideCappedOpaque(t *testing.T) {
	ld, cg := loadFixture(t)
	r := NewResolver(cg, DefaultFanoutCap, nil)
	site := findInvoke(t, ld.Prog, "HandleWide", "Encode")

	targets, conf, capped := r.TargetsAt(site)
	if !capped {
		t.Fatalf("wide site (12 impls) not capped at %d", DefaultFanoutCap)
	}
	if targets != nil || conf != 1.0 {
		t.Fatalf("capped site: targets=%v conf=%v, want nil/1.0", targets, conf)
	}
}

func TestCapInjectable(t *testing.T) {
	ld, cg := loadFixture(t)
	r := NewResolver(cg, 1, nil)
	site := findInvoke(t, ld.Prog, "HandleNarrow", "Save")
	if _, _, capped := r.TargetsAt(site); !capped {
		t.Fatalf("cap=1 did not cap a 2-target site")
	}
}

func TestNilResolverAndSite(t *testing.T) {
	if NewResolver(nil, DefaultFanoutCap, nil) != nil {
		t.Fatalf("NewResolver(nil) != nil")
	}
	var r *Resolver
	if targets, conf, capped := r.TargetsAt(nil); targets != nil || conf != 1.0 || capped {
		t.Fatalf("nil resolver: got %v %v %v, want nil 1.0 false", targets, conf, capped)
	}
}

func TestDeterministicAcrossGraphBuilds(t *testing.T) {
	ld, cg := loadFixture(t)
	site := findInvoke(t, ld.Prog, "HandleNarrow", "Save")
	g2 := vta.CallGraph(ssautil.AllFunctions(ld.Prog), nil)

	t1, _, _ := NewResolver(cg, DefaultFanoutCap, nil).TargetsAt(site)
	t2, _, _ := NewResolver(g2, DefaultFanoutCap, nil).TargetsAt(site)
	a, b := fqns(t1), fqns(t2)
	if strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatalf("target order differs across graph builds: %v vs %v", a, b)
	}
}

// TestKeepDecidesSyntheticTargets pins the W0 contract (doc 24 N1): when emit
// supplies its emittability predicate, that predicate is the ONLY authority, so
// a synthetic wrapper ($bound / $thunk / promotion) can be a dispatch target.
// Dropping wrappers here left method-value call sites with zero targets, hence
// opaque, hence blind to the whole callee body.
//
// The keep == nil fallback must keep rejecting them: every other test in this
// package, plus flow and snapshot tests, construct a resolver without emit's
// predicate and rely on the historical behaviour.
func TestKeepDecidesSyntheticTargets(t *testing.T) {
	ld, cg := loadFixture(t)

	var wrapper *ssa.Function
	for f := range ssautil.AllFunctions(ld.Prog) {
		if f.Synthetic != "" && len(f.TypeArgs()) == 0 && len(f.Blocks) > 0 {
			wrapper = f
			break
		}
	}
	if wrapper == nil {
		t.Skip("no synthetic wrapper in this fixture")
	}

	if r := NewResolver(cg, DefaultFanoutCap, nil); r.usable(wrapper) {
		t.Errorf("keep==nil must still reject synthetic %s", wrapper)
	}
	yes := NewResolver(cg, DefaultFanoutCap, func(*ssa.Function) bool { return true })
	if !yes.usable(wrapper) {
		t.Errorf("keep==true must accept synthetic %s (W0)", wrapper)
	}
	no := NewResolver(cg, DefaultFanoutCap, func(*ssa.Function) bool { return false })
	if no.usable(wrapper) {
		t.Errorf("keep==false must reject %s — keep is the only authority", wrapper)
	}
	if no.usable(nil) || yes.usable(nil) {
		t.Errorf("nil target must never be usable")
	}
}
