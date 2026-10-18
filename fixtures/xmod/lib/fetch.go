// Package lib is a dependency of the app module. The app calls it across a
// module boundary, so the iid of each function here must be the same whether
// lib is extracted on its own or called from the app.
package lib

import (
	"context"
	"database/sql"
)

// DB is the handle Fetch queries.
var DB *sql.DB

// Fetch builds its query by concatenation on purpose: id reaches a SQL sink.
func Fetch(id string) string {
	var name string
	_ = DB.QueryRowContext(context.Background(), "SELECT name FROM users WHERE id = '"+id+"'").Scan(&name)
	return name
}
