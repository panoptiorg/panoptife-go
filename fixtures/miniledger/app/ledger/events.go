package ledger

// handleEventsBatch is the event handler; retryEvent re-enters it through the
// enqueue func value, closing the handleEventsBatch→retryEvent→closure cycle —
// a 3-member SCC stitched entirely from dynamic edges, visible only under
// vta/cha.
func (i *Implementation) handleEventsBatch(batch []string) error {
	for _, e := range batch {
		if err := i.store.SaveOsv(e); err != nil {
			return i.retryEvent(e)
		}
	}
	return nil
}

func (i *Implementation) retryEvent(e string) error {
	if len(e) > 32 {
		return nil
	}
	// Generics marker: pool.Add is a generic instantiation. Since the generics
	// fix the frontend emits instances (Pool[string].Add/flush in the CGF), so
	// this call joins the Add→flush→closure→handleEventsBatch→retryEvent cycle
	// (the size>=5 SCC e2e-miniledger asserts). Pre-fix it was a default-leaf
	// and the cycle never formed.
	if err := i.pool.Add(e + ":retry"); err != nil {
		return err
	}
	return i.enqueue(e + ":retry")
}
