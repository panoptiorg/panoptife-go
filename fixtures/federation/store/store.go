package store

type Storage struct{}

// Selectx models a pgx-wrapper SQL exec sink local to the federation service.
func (s *Storage) Selectx(query string) (string, error) {
	return "row:" + query, nil
}
