package backup

type Storage interface {
	BackupOsv(id string) ([]string, error)
}

type Step struct{ s Storage }

func New(s Storage) *Step { return &Step{s: s} }

func (st *Step) Run(date string) error {
	_, err := st.s.BackupOsv(date)
	return err
}
