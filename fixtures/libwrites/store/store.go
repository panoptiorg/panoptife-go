package store

// DB.Selectx is the SQL sink (catalog `\.Selectx$`, class sqli).
type DB struct{}

func (d *DB) Selectx(query string) error { return nil }
