package store

type Storage struct{}

// Selectx models a pgx-wrapper SQL exec sink: `query` is the tainted-carrying arg.
func (s *Storage) Selectx(query string) (string, error) {
	return "row:" + query, nil
}
