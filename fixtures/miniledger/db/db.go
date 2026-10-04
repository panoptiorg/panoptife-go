// Package db models the pgx-wrapper executor acme stores wrap. The query argument
// of Getx/Selectx/Execx/Queryx/Getxx is the sink (catalog sqli selector_regex);
// these five names appear nowhere else in the fixture.
package db

type DB struct{ dsn string }

func New(dsn string) *DB { return &DB{dsn: dsn} }

func (d *DB) Getx(query string) (string, error) { return "row:" + query, nil }

func (d *DB) Selectx(query string) ([]string, error) { return []string{query}, nil }

func (d *DB) Execx(query string) error { _ = query; return nil }

func (d *DB) Queryx(query string) ([]string, error) { return []string{query}, nil }

func (d *DB) Getxx(query string) (int, error) { return len(query), nil }
