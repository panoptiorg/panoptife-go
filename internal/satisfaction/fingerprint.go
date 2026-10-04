// Package satisfaction materializes the structural `type ⊨ interface`
// relation and its method-set fingerprints from HEAD type-checker facts
// (never predicted from diff text — the spec §5 embedding/promotion hazard is
// resolved by types.NewMethodSet computing promotion transitively). Ph1 only
// persists these into the cgstore snapshot; the Ph2+ satisfaction delta diffs
// them against the base commit's table.
package satisfaction

import (
	"crypto/sha256"
	"encoding/binary"
	"go/types"
	"sort"
)

// pathQual qualifies type strings by full package path — scope-independent,
// collision-free keys.
func pathQual(p *types.Package) string { return p.Path() }

// TypeKey is the canonical persisted identity of a named type.
func TypeKey(t types.Type) string {
	return types.TypeString(t, pathQual)
}

// Msfp fingerprints the method set of named type T: sha256 over the POINTER
// method set (the superset) sorted by types.Func.Id(), each entry
// Id ⊕ canonical signature ⊕ an "also in the value set" bit — the
// pointer-vs-value receiver hazard folds into one hash. Embedded/promoted
// methods are included transitively by NewMethodSet.
func Msfp(named types.Type) []byte {
	ptr := types.NewMethodSet(types.NewPointer(named))
	val := types.NewMethodSet(named)
	inVal := make(map[string]bool, val.Len())
	for i := 0; i < val.Len(); i++ {
		if f, ok := val.At(i).Obj().(*types.Func); ok {
			inVal[f.Id()] = true
		}
	}
	type entry struct{ id, sig, set string }
	entries := make([]entry, 0, ptr.Len())
	for i := 0; i < ptr.Len(); i++ {
		f, ok := ptr.At(i).Obj().(*types.Func)
		if !ok {
			continue
		}
		set := "p"
		if inVal[f.Id()] {
			set = "pv"
		}
		entries = append(entries, entry{f.Id(), types.TypeString(f.Type(), pathQual), set})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].id < entries[j].id })
	h := sha256.New()
	for _, e := range entries {
		writeField(h, e.id)
		writeField(h, e.sig)
		writeField(h, e.set)
	}
	return h.Sum(nil)
}

// Ifp fingerprints an interface: sha256 over its complete (embedding-
// flattened) method set sorted by Id.
func Ifp(iface *types.Interface) []byte {
	type entry struct{ id, sig string }
	entries := make([]entry, 0, iface.NumMethods())
	for i := 0; i < iface.NumMethods(); i++ {
		f := iface.Method(i)
		entries = append(entries, entry{f.Id(), types.TypeString(f.Type(), pathQual)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].id < entries[j].id })
	h := sha256.New()
	for _, e := range entries {
		writeField(h, e.id)
		writeField(h, e.sig)
	}
	return h.Sum(nil)
}

func writeField(h interface{ Write([]byte) (int, error) }, s string) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(s)))
	h.Write(n[:])
	h.Write([]byte(s))
}
