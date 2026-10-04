package osvreview

type Storage interface {
	ReviewOsv(id string) error
}

type Step struct{ s Storage }

func New(s Storage) *Step { return &Step{s: s} }

func (st *Step) Run(date string) error {
	return st.s.ReviewOsv(date)
}
