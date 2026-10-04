package accrue

// Storage is the package-local interface subset acme EOD steps declare
// (cf. ledger-svc endofday/* — ~42 such interfaces, all backed by the one
// concrete *store.Storage).
type Storage interface {
	AccrueReserve(day string) error
}

type Step struct{ s Storage }

func New(s Storage) *Step { return &Step{s: s} }

func (st *Step) Run(date string) error {
	return st.s.AccrueReserve(date)
}
