package hash

import (
	"encoding/hex"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// instanceView is what pc-fe emits about one instance-like function.
type instanceView struct {
	raw string // fn.String() + fn.Signature.String(): what IID hashed before
	fqn string // FQN(fn)
	iid string // IID(repo, fn)
	sig string // SignatureString(fn.Signature)
}

// buildInOrder builds testdata/instances with InstantiateGenerics, building
// the named packages in exactly this order — the only thing that differs
// between two calls. pc-fe builds packages in parallel, so in production the
// order is a race; here it is pinned, so the test is deterministic.
func buildInOrder(t *testing.T, order ...string) map[string]instanceView {
	t.Helper()
	cfg := &packages.Config{Dir: "testdata/instances", Mode: packages.LoadAllSyntax}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	prog, ssaPkgs := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	byName := map[string]*ssa.Package{}
	for _, p := range ssaPkgs {
		if p != nil {
			byName[p.Pkg.Name()] = p
		}
	}
	for _, name := range order {
		p := byName[name]
		if p == nil {
			t.Fatalf("no package %q", name)
		}
		p.Build()
	}
	out := map[string]instanceView{}
	for fn := range ssautil.AllFunctions(prog) {
		if !IsInstanceLike(fn) {
			continue
		}
		v := instanceView{
			raw: fn.String() + " " + fn.Signature.String(),
			fqn: FQN(fn),
			iid: hex.EncodeToString(IID("repo", fn)),
			sig: SignatureString(fn.Signature),
		}
		if prev, dup := out[v.fqn]; dup && prev.iid != v.iid {
			t.Fatalf("one canonical name, two iids: %s", v.fqn)
		}
		out[v.fqn] = v
	}
	return out
}

// The bug, pinned: the same program built in two package orders gave
// instances different printed names/signatures and therefore different iids.
// With CanonicalInstances every instance has one name and one iid whatever the
// order — and the raw ssa strings DO differ between the orders, so this test
// would catch a regression rather than pass vacuously.
func TestInstanceIdentityIsBuildOrderIndependent(t *testing.T) {
	CanonicalInstances = true
	ab := buildInOrder(t, "pa", "pb", "plain")
	ba := buildInOrder(t, "pb", "pa", "plain")

	rawDiffers := 0
	for fqn, v := range ab {
		w, ok := ba[fqn]
		if !ok {
			t.Errorf("%s: canonical name present in one order only", fqn)
			continue
		}
		if v.iid != w.iid || v.sig != w.sig {
			t.Errorf("%s: order changed the identity: %s %q vs %s %q", fqn, v.iid[:12], v.sig, w.iid[:12], w.sig)
		}
		if v.raw != w.raw {
			rawDiffers++
		}
	}
	if len(ab) != len(ba) {
		t.Errorf("instance sets differ: %d vs %d", len(ab), len(ba))
	}
	if rawDiffers == 0 {
		t.Fatal("fixture broken: no instance's raw ssa name/signature depends on build order, so this test proves nothing")
	}

	// the two shapes measured on a real repo, by name
	for _, want := range []string{
		"example.com/instances/lo.Contains[string]",
		"slices.Contains[[]string string]",
		"(*example.com/instances/lo.Set[string]).Add[string]",
	} {
		if _, ok := ab[want]; !ok {
			t.Errorf("missing canonical instance %s; have %s", want, strings.Join(keys(ab), ", "))
		}
	}
	// the alias spelling is one identity class with string: no separate name
	for fqn := range ab {
		if strings.Contains(fqn, "model.Str") {
			t.Errorf("alias spelling leaked into a canonical name: %s", fqn)
		}
	}
}

// For an instance with no alias, no named parameter inside a function-typed
// type argument and no non-empty interface type argument, the canonical name is
// exactly ssa's — so turning canonicalization on renames nothing that was
// already unambiguous.
func TestCanonicalNameEqualsSSAWhenUnambiguous(t *testing.T) {
	CanonicalInstances = true
	cfg := &packages.Config{Dir: "testdata/instances", Mode: packages.LoadAllSyntax}
	pkgs, err := packages.Load(cfg, "./plain")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	prog, ssaPkgs := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	for _, p := range ssaPkgs {
		p.Build()
	}
	n := 0
	for fn := range ssautil.AllFunctions(prog) {
		if !IsInstanceLike(fn) || !strings.Contains(fn.String(), "plain.ID") {
			continue
		}
		n++
		if got := FQN(fn); got != fn.String() {
			t.Errorf("canonical %q != ssa %q", got, fn.String())
		}
	}
	if n < 4 { // Contains, Map, Map's closure, Set.Add
		t.Fatalf("fixture broken: only %d plain instances", n)
	}
}

// Off reproduces the previous identity exactly.
func TestCanonicalInstancesOffIsTheOldIdentity(t *testing.T) {
	defer func() { CanonicalInstances = true }()
	CanonicalInstances = false
	cfg := &packages.Config{Dir: "testdata/instances", Mode: packages.LoadAllSyntax}
	pkgs, err := packages.Load(cfg, "./pb")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	prog, ssaPkgs := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	for _, p := range ssaPkgs {
		p.Build()
	}
	for fn := range ssautil.AllFunctions(prog) {
		if !IsInstanceLike(fn) {
			continue
		}
		if FQN(fn) != fn.String() {
			t.Errorf("off: FQN %q != %q", FQN(fn), fn.String())
		}
		old := IIDFromParts("repo", PackagePath(fn), fn.String(), fn.Signature.String())
		if hex.EncodeToString(IID("repo", fn)) != hex.EncodeToString(old) {
			t.Errorf("off: %s iid is not the old formula", fn)
		}
	}
}

func keys(m map[string]instanceView) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
