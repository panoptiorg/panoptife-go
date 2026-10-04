// Package store models the pgx-wrapper executor other fixtures use. Selectx
// and Execx are the sink names the public catalog's in-house-wrapper rule
// keys on — a store package that merely forwards to these names is treated
// as a sink itself, same as backend/dispatch/miniledger.
package store

type Storage struct{}

// Selectx models a pgx-wrapper SQL read sink: `query` is the tainted-carrying arg.
func (s *Storage) Selectx(query string) (string, error) {
	return "row:" + query, nil
}

// Execx models a pgx-wrapper SQL exec (mutation) sink.
func (s *Storage) Execx(query string) error {
	_ = query
	return nil
}
