package store

// MaskDB / NoteDB give two SAME-CLASS (sqli) sinks distinguishable in a route by
// their callee fqn — the tie-break-independence device (HANDOFF trap 4).
type MaskDB struct{}

func (d *MaskDB) Selectx(query string) error { return nil }

type NoteDB struct{}

func (d *NoteDB) Selectx(query string) error { return nil }

// Untainted runtime value: a call result, so SSA keeps it a value rather than
// folding a constant.
func Notes() string { return "audit" }
