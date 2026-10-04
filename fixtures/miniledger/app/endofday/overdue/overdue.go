package overdue

type Storage interface {
	ListReserves(day string) ([]string, error)
}

type Step struct{ s Storage }

func New(s Storage) *Step { return &Step{s: s} }

func (st *Step) Run(date string) error {
	_, err := st.s.ListReserves(date)
	return err
}
