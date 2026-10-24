package flow

import (
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/panoptiorg/panoptife-go/internal/loader"
)

// loadFuncVars loads the funcvars fixture, builds VarInits over it, and
// returns the Result for the named function.
func loadFuncVars(t *testing.T, fqn string, withVarInits bool) *Result {
	t.Helper()
	ld, err := loader.Load("testdata/funcvars", "./...", false, true, false, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
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
	opts := Opts{FieldPaths: true, ClosureFlow: true}
	if withVarInits {
		opts.VarInits = BuildVarInitIndex(ld.Prog)
	}
	return Build(fn, nil, opts)
}

// The fixture's core case: `var Clean = Normalize`, bound exactly once in
// the package initializer, turns the call in CallClean into a static call
// named by Normalize's fqn — the name a catalog rule selects.
func TestFuncVarBoundInInitializer(t *testing.T) {
	res := loadFuncVars(t, "example.com/funcvars.CallClean", true)
	if len(res.Flow.Callsites) != 1 {
		t.Fatalf("callsites = %d, want 1", len(res.Flow.Callsites))
	}
	cs := res.Flow.Callsites[0]
	if cs.CalleeFqn != "example.com/funcvars.Normalize" {
		t.Errorf("callee fqn = %q, want example.com/funcvars.Normalize", cs.CalleeFqn)
	}
	if res.Callees[0].Static == nil {
		t.Errorf("callee must resolve statically")
	}
	if cs.Opaque || cs.Arg0IsReceiver {
		t.Errorf("opaque=%v arg0_is_receiver=%v, want false/false", cs.Opaque, cs.Arg0IsReceiver)
	}
}

// Without VarInits the emission is byte-for-byte the old one: the call stays
// dynamic, named by the variable's type string.
func TestFuncVarIndexNilKeepsOldEmission(t *testing.T) {
	res := loadFuncVars(t, "example.com/funcvars.CallClean", false)
	cs := res.Flow.Callsites[0]
	if cs.CalleeFqn != "func(s string) string" {
		t.Errorf("callee fqn = %q, want the type string", cs.CalleeFqn)
	}
	if res.Callees[0].Static != nil || !cs.Opaque {
		t.Errorf("static=%v opaque=%v, want nil/true", res.Callees[0].Static != nil, cs.Opaque)
	}
}

// A second store — the rebind in Swap — keeps every call through Rebound
// dynamic, even one the call graph would resolve to a single target.
func TestFuncVarStoredTwiceStaysDynamic(t *testing.T) {
	res := loadFuncVars(t, "example.com/funcvars.CallRebound", true)
	cs := res.Flow.Callsites[0]
	if res.Callees[0].Static != nil {
		t.Errorf("Rebound has two stores; call must stay dynamic")
	}
	if !cs.Opaque || cs.CalleeFqn == "example.com/funcvars.Normalize" {
		t.Errorf("callee fqn = %q opaque=%v, want a type-string name, not the function fqn", cs.CalleeFqn, cs.Opaque)
	}
}

// A variable stored only from a function has no initializer store: its calls
// stay dynamic.
func TestFuncVarSetOutsideInitializerStaysDynamic(t *testing.T) {
	res := loadFuncVars(t, "example.com/funcvars.CallSetOnly", true)
	var found bool
	for i, cs := range res.Flow.Callsites {
		if cs.CalleeFqn == "example.com/funcvars.SetOnlyFn" {
			continue // the static helper call
		}
		found = true
		if res.Callees[i].Static != nil {
			t.Errorf("SetOnly is stored from a function; call must stay dynamic")
		}
		if cs.CalleeFqn == "example.com/funcvars.Normalize" {
			t.Errorf("SetOnly call named by the function fqn %q", cs.CalleeFqn)
		}
	}
	if !found {
		t.Fatalf("no dynamic callsite for SetOnly (callsites: %+v)", res.Flow.Callsites)
	}
}

// An initializer-bound variable whose address reaches another function is
// not provably constant: whoever holds the pointer may store through it, so
// the call stays dynamic even though the variable itself has one store.
func TestFuncVarAddressEscapeStaysDynamic(t *testing.T) {
	res := loadFuncVars(t, "example.com/funcvars.CallEscaped", true)
	var found bool
	for i, cs := range res.Flow.Callsites {
		if cs.CalleeFqn == "example.com/funcvars.TakeAddr" {
			continue // the static helper call
		}
		found = true
		if res.Callees[i].Static != nil {
			t.Errorf("Escaped's address escapes to TakeAddr; call must stay dynamic")
		}
		if cs.CalleeFqn == "example.com/funcvars.Normalize" {
			t.Errorf("Escaped call named by the function fqn %q", cs.CalleeFqn)
		}
	}
	if !found {
		t.Fatalf("no dynamic callsite for Escaped (callsites: %+v)", res.Flow.Callsites)
	}
}

// A store through a pointer alias (`p := &v; *p = f2`) is a second store to
// the variable: the call stays dynamic.
func TestFuncVarRebindViaPointerStaysDynamic(t *testing.T) {
	res := loadFuncVars(t, "example.com/funcvars.CallReboundViaPtr", true)
	cs := res.Flow.Callsites[0]
	if res.Callees[0].Static != nil {
		t.Errorf("ReboundViaPtr is stored twice; call must stay dynamic")
	}
	if !cs.Opaque || cs.CalleeFqn == "example.com/funcvars.Normalize" {
		t.Errorf("callee fqn = %q opaque=%v, want a type-string name, not the function fqn", cs.CalleeFqn, cs.Opaque)
	}
}

// A method expression bound once (`var MethodExpr = (*Thing).Greet`) names
// the METHOD, not the $thunk wrapper: same fqn a catalog rule selects, same
// arg shape as a direct method call — receiver at arg 0.
func TestFuncVarMethodExpression(t *testing.T) {
	res := loadFuncVars(t, "example.com/funcvars.CallMethodExpr", true)
	if len(res.Flow.Callsites) != 1 {
		t.Fatalf("callsites = %d, want 1", len(res.Flow.Callsites))
	}
	cs := res.Flow.Callsites[0]
	if cs.CalleeFqn != "(*example.com/funcvars.Thing).Greet" {
		t.Errorf("callee fqn = %q, want (*example.com/funcvars.Thing).Greet", cs.CalleeFqn)
	}
	if res.Callees[0].Static == nil {
		t.Errorf("callee must resolve statically to the method")
	}
	if !cs.Arg0IsReceiver {
		t.Errorf("arg0_is_receiver = false, want true (the method's receiver)")
	}
	if cs.Opaque {
		t.Errorf("callsite must not be opaque")
	}
}
