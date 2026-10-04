package store

// Tx models the executor handed to a transaction closure.
type Tx struct{}

// Selectx on the tx is the sink the common transaction idiom hits
// (ledger-svc store/documents.go:252). Catalog class "sqli".
func (t *Tx) Selectx(query string) error { return nil }

// WithTx is the transaction wrapper: it takes the closure as a VALUE and calls
// it. The func value is `cc.Value`, not one of `cc.Args`, so no arg port exists
// for it — which is precisely why a capture cannot cross here and has to be
// bound at the *ssa.MakeClosure site instead.
func WithTx(fn func(tx *Tx) error) error { return fn(&Tx{}) }

// MaskDB / NoteDB give two SAME-CLASS (sqli) sinks that are distinguishable in
// a route by their callee fqn — the tie-break-independence device
// (HANDOFF trap 4, same shape as fixtures/fieldpath ViaMask/ViaNote).
type MaskDB struct{}

func (d *MaskDB) Selectx(query string) error { return nil }

type NoteDB struct{}

func (d *NoteDB) Selectx(query string) error { return nil }

// Notes is an untainted runtime value: a call result, so ssa really does make
// it a captured variable (a bare constant might not survive as a FreeVar).
func Notes() string { return "audit" }
