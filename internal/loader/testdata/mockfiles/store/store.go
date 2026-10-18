package store

// Store reads names.
type Store interface{ Get(id string) string }

// SQL is the real store.
type SQL struct{}

// Get returns the name.
func (SQL) Get(id string) string { return id }
