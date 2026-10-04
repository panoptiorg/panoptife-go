package satisfaction

import (
	"go/types"
	"sort"
)

// TypeRow / IfaceRow / Pair are the persisted satisfaction facts, each tagged
// with the declaring (for Pair: implementing) package for shard grouping.
type TypeRow struct {
	Pkg  string
	Key  string
	Msfp []byte
}

type IfaceRow struct {
	Pkg string
	Key string
	Ifp []byte
}

type Pair struct {
	Pkg         string // implementing type's package (shard key)
	TypeKey     string
	IfaceKey    string
	PointerOnly bool
}

// Index is the materialized relation over the in-scope package set.
//
// Ph1 scope note: types AND interfaces are enumerated from in-scope package
// scopes only. Cross-package satisfaction between two in-scope packages (the
// measured-89% hazard) is covered; interfaces declared in dep packages
// (generated pb, stdlib) are NOT — Ph2's delta consumer must widen this or
// conservatively invalidate on dep changes (dep changes bump go.sum, which is
// already in the store key, so Ph1 correctness is unaffected).
type Index struct {
	Types  []TypeRow
	Ifaces []IfaceRow
	Pairs  []Pair
}

// Build enumerates named types/interfaces declared in pkgs and computes
// fingerprints + satisfaction pairs. Candidate pruning: an interface is only
// checked against types that have its (arbitrary) first method's Id in their
// pointer method set — the reverse-index idea from spec §5 step 2.
func Build(pkgs []*types.Package) *Index {
	idx := &Index{}
	sorted := append([]*types.Package(nil), pkgs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path() < sorted[j].Path() })

	type namedType struct {
		named *types.Named
		key   string
		pkg   string
	}
	var concretes []namedType
	var ifaces []struct {
		iface *types.Interface
		key   string
		pkg   string
	}
	// methodID -> indices into concretes whose pointer method set contains it
	byMethod := map[string][]int{}

	for _, p := range sorted {
		scope := p.Scope()
		for _, name := range scope.Names() { // Names() is sorted
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || tn.IsAlias() {
				continue
			}
			named, ok := tn.Type().(*types.Named)
			if !ok {
				continue
			}
			if iface, ok := named.Underlying().(*types.Interface); ok {
				if iface.NumMethods() == 0 {
					continue // interface{} matches everything; useless fact
				}
				idx.Ifaces = append(idx.Ifaces, IfaceRow{Pkg: p.Path(), Key: TypeKey(named), Ifp: Ifp(iface)})
				ifaces = append(ifaces, struct {
					iface *types.Interface
					key   string
					pkg   string
				}{iface, TypeKey(named), p.Path()})
				continue
			}
			ms := types.NewMethodSet(types.NewPointer(named))
			if ms.Len() == 0 {
				continue // no methods: can never satisfy a non-empty interface
			}
			i := len(concretes)
			concretes = append(concretes, namedType{named, TypeKey(named), p.Path()})
			idx.Types = append(idx.Types, TypeRow{Pkg: p.Path(), Key: TypeKey(named), Msfp: Msfp(named)})
			for j := 0; j < ms.Len(); j++ {
				if f, ok := ms.At(j).Obj().(*types.Func); ok {
					byMethod[f.Id()] = append(byMethod[f.Id()], i)
				}
			}
		}
	}

	for _, ir := range ifaces {
		for _, ci := range byMethod[ir.iface.Method(0).Id()] {
			c := concretes[ci]
			ptrOK := types.Implements(types.NewPointer(c.named), ir.iface)
			if !ptrOK {
				continue
			}
			valOK := types.Implements(c.named, ir.iface)
			idx.Pairs = append(idx.Pairs, Pair{
				Pkg: c.pkg, TypeKey: c.key, IfaceKey: ir.key, PointerOnly: !valOK,
			})
		}
	}
	sort.Slice(idx.Pairs, func(i, j int) bool {
		a, b := idx.Pairs[i], idx.Pairs[j]
		if a.TypeKey != b.TypeKey {
			return a.TypeKey < b.TypeKey
		}
		return a.IfaceKey < b.IfaceKey
	})
	return idx
}
