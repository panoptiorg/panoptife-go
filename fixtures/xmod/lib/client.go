package lib

import (
	"context"
	"database/sql"
)

// Client is the interface New returns. The concrete type stays inside lib, so
// code that calls New never sees it: the conversion to Client is in a body the
// caller's analysis does not build.
type Client interface {
	Do(q string) string
	Close() error
}

type client struct{ db *sql.DB }

// Do builds its query by concatenation on purpose: q reaches a SQL sink.
func (c *client) Do(q string) string {
	var name string
	_ = c.db.QueryRowContext(context.Background(), "SELECT name FROM users WHERE id = '"+q+"'").Scan(&name)
	return name
}

func (c *client) Close() error { return c.db.Close() }

// New returns a Client that queries DB.
func New() Client { return &client{db: DB} }
