package app

import "example.com/dispatch/store"

// Repo is a NARROW interface (2 impls): dispatch resolves it, so the sink
// buried inside PgRepo.Save is only reachable when targets are enumerated —
// the default leaf is taint-transparent but sees no sinks inside callees.
type Repo interface {
	Save(q string) (string, error)
}

type PgRepo struct{ st *store.Storage }

func (r *PgRepo) Save(q string) (string, error) { return r.st.Selectx(q) }

type MemRepo struct{ data []string }

func (m *MemRepo) Save(q string) (string, error) {
	m.data = append(m.data, q)
	return "", nil
}
