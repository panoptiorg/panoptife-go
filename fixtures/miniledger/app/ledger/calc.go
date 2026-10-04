package ledger

// ICalc's methods call each other through the interface field, so the
// Recalc→Rebuild→Audit→Recalc cycle exists only when dispatch is resolved:
// a 3-member SCC the engine must fixpoint and can never cache.
type ICalc interface {
	Recalc(id string) error
	Rebuild(id string) error
	Audit(id string) error
}

func (i *Implementation) Recalc(id string) error {
	if len(id) > 8 {
		return i.store.RecalcReserve(id)
	}
	return i.calc.Rebuild(id + "r")
}

func (i *Implementation) Rebuild(id string) error {
	return i.calc.Audit(id + "a")
}

func (i *Implementation) Audit(id string) error {
	return i.calc.Recalc(id + "c")
}

// walkDepth is directly self-recursive: never cached (cacheable requires
// non-self-recursive) but not a multi-member SCC, so no scc-start line —
// the third recursion flavour worth seeing in a trace.
func (i *Implementation) walkDepth(n int, q string) error {
	if n <= 0 {
		_, err := i.raw.GetOsv(q)
		return err
	}
	return i.walkDepth(n-1, q+"/d")
}
