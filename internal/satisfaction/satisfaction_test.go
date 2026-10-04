package satisfaction

import (
	"bytes"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

const src = `package p

type I interface{ Foo() }
type J interface{ Foo(); Bar() }
type Empty interface{}

type A struct{}
func (A) Foo() {}

type B struct{}
func (*B) Foo() {}
func (*B) Bar() {}

type Emb struct{ A } // promotion: Emb satisfies I via the embedded A
`

func checkPkg(t *testing.T, source string) *types.Package {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	conf := types.Config{Importer: importer.Default()}
	pkg, err := conf.Check("example.com/p", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

func pairSet(idx *Index) map[string]bool {
	m := map[string]bool{}
	for _, p := range idx.Pairs {
		key := p.TypeKey + "⊨" + p.IfaceKey
		if p.PointerOnly {
			key += "*"
		}
		m[key] = true
	}
	return m
}

func TestSatisfactionPairs(t *testing.T) {
	idx := Build([]*types.Package{checkPkg(t, src)})
	got := pairSet(idx)
	for _, want := range []string{
		"example.com/p.A⊨example.com/p.I",     // value receiver: not pointer-only
		"example.com/p.B⊨example.com/p.I*",    // pointer receivers: pointer-only
		"example.com/p.B⊨example.com/p.J*",    //
		"example.com/p.Emb⊨example.com/p.I",   // via embedding/promotion
	} {
		if !got[want] {
			t.Errorf("missing pair %s (got %v)", want, got)
		}
	}
	for k := range got {
		if bytes.Contains([]byte(k), []byte("Empty")) {
			t.Errorf("empty interface must not produce pairs: %s", k)
		}
	}
}

func TestMsfpDeterministicAndPromotionAware(t *testing.T) {
	p1, p2 := checkPkg(t, src), checkPkg(t, src) // two independent type-checks
	get := func(p *types.Package, name string) types.Type {
		return p.Scope().Lookup(name).(*types.TypeName).Type()
	}
	if !bytes.Equal(Msfp(get(p1, "A")), Msfp(get(p2, "A"))) {
		t.Fatal("msfp not deterministic across independent type-checks")
	}
	if bytes.Equal(Msfp(get(p1, "A")), Msfp(get(p1, "B"))) {
		t.Fatal("msfp(A) == msfp(B): value-set bit or method list not folded in")
	}
	// Emb's promoted method set is {Foo()} with a value receiver — identical
	// shape to A's, so their fingerprints must coincide (promotion is resolved
	// transitively by NewMethodSet, not visible as embedding structure).
	if !bytes.Equal(Msfp(get(p1, "Emb")), Msfp(get(p1, "A"))) {
		t.Fatal("msfp(Emb) != msfp(A): promotion not folded transitively")
	}
}

func TestIfpDistinguishesInterfaces(t *testing.T) {
	p := checkPkg(t, src)
	iface := func(name string) *types.Interface {
		return p.Scope().Lookup(name).(*types.TypeName).Type().Underlying().(*types.Interface)
	}
	if bytes.Equal(Ifp(iface("I")), Ifp(iface("J"))) {
		t.Fatal("ifp(I) == ifp(J)")
	}
	if !bytes.Equal(Ifp(iface("I")), Ifp(iface("I"))) {
		t.Fatal("ifp not deterministic")
	}
}
