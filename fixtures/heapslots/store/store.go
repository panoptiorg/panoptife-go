package store

// MaskDB / NoteDB give two SAME-CLASS (sqli) sinks distinguishable in a route by
// their callee fqn — the tie-break-independence device (HANDOFF trap 4, the
// shape fixtures/fieldpath ViaMask/ViaNote and fixtures/closureflow use).
type MaskDB struct{}

func (d *MaskDB) Selectx(query string) error { return nil }

type NoteDB struct{}

func (d *NoteDB) Selectx(query string) error { return nil }

// Getx takes `any`, so an interface-typed cell's value can reach a sink WITHOUT
// a type assertion — the A16 recall guard (doc 30 §7b: the blunt filter would
// delete this flow, the precise narrowing must keep it). Same sqli class,
// different callee fqn, so a route that lands here is distinguishable.
func (d *NoteDB) Getx(v any) error { return nil }

// Untainted runtime value: a call result, so SSA really keeps it as a value
// rather than folding a constant.
func Notes() string { return "audit" }
