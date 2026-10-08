// Package store is the SQL layer every router in this fixture writes to. The
// queries are built by concatenation on purpose: they are the sinks the
// cross-repo chains end at.
package store

import (
	"context"
	"database/sql"
)

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) CreateUser(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO users(name) VALUES ('"+name+"')")
	return err
}

func (s *Store) FindUser(ctx context.Context, id string) (string, error) {
	var name string
	err := s.db.QueryRowContext(ctx, "SELECT name FROM users WHERE id = '"+id+"'").Scan(&name)
	return name, err
}

// CountUsers runs a constant query: only ctx reaches the sink. A design that
// taints the whole request turns this into a finding (coverage wave 1, E3).
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT count(*) FROM users")
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	return n, nil
}

func (s *Store) CreateItem(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO items(name) VALUES ('"+name+"')")
	return err
}

func (s *Store) Exec(ctx context.Context, q string) error {
	_, err := s.db.ExecContext(ctx, q)
	return err
}
