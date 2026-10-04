// Package app exercises CLOSURE FREE VARIABLES (doc 24 §5 W1d / doc 26 §5).
//
// Before W1d, `fn.FreeVars` was never referenced in the frontend, so a value a
// closure needs from its creation scope entered it nowhere: the closure's own
// body was summarized (it has its own sink_hits), but the captured value had no
// in-slot, and the invocation site could not supply one either — a closure is
// called through `cc.Value`, which is not an arg port. Every one of the four
// handlers below therefore produced ZERO chains before W1d.
//
// This is the gap that made four of Stage C's six removals dishonest
// (doc 26 §5): the real `Queryx` inside `withTx(…, func(ctx, tx) error { … })`
// at ledger-svc store/documents.go:250-252 was unreachable, so the finding only
// survived through an unrelated widened branch.
package app

import (
	"context"

	"example.com/closureflow/pb"
	"example.com/closureflow/store"
)

type Implementation struct {
	pb.UnimplementedClosureServer
}

func New() *Implementation { return &Implementation{} }

// HandleTx is the common transaction idiom: the query is built OUTSIDE the closure and
// captured; the SQL exec happens INSIDE it. Zero chains before W1d, one after,
// and the route must descend through the closure to reach (*Tx).Selectx.
func (i *Implementation) HandleTx(ctx context.Context, req *pb.ClosureRequest) error {
	q := "SELECT * FROM t WHERE q = " + req.GetQuery()
	return store.WithTx(func(tx *store.Tx) error {
		return tx.Selectx(q) // sqli SINK — `q` is the FreeVar
	})
}

type holder struct{ q string }

func (h *holder) run() error { return (&store.MaskDB{}).Selectx(h.q) }

// HandleBoundRecv captures through a `$bound` wrapper, whose single free var is
// the RECEIVER. W0 made the wrapper visible; only W1d gets the receiver into
// it. `f()` passes zero args, so there is no arg port to carry it.
func (i *Implementation) HandleBoundRecv(ctx context.Context, req *pb.ClosureRequest) error {
	h := &holder{q: req.GetQuery()}
	f := h.run // MakeClosure((*holder).run$bound, [h])
	return f()
}

// HandleTwoCaptures is the tie-break-independence guard (HANDOFF trap 4). ONE
// closure with TWO captures and two SAME-CLASS sinks, one per capture — only
// `m` is tainted. If the binding arg indices were shifted by one, or the two
// bindings were paired with the wrong free vars, the fact would land on `n`'s
// slot and the route would terminate at (*NoteDB).Selectx instead. No sort
// order makes that assertion pass vacuously.
func (i *Implementation) HandleTwoCaptures(ctx context.Context, req *pb.ClosureRequest) error {
	m := req.GetMaskEq()
	n := store.Notes() // untainted, but a real captured variable
	return store.WithTx(func(tx *store.Tx) error {
		if err := (&store.NoteDB{}).Selectx("note = " + n); err != nil {
			return err
		}
		return (&store.MaskDB{}).Selectx("mask = " + m)
	})
}

// HandleUncaptured is the precision negative: the request IS tainted and a
// closure IS created with a binding, but the binding is untainted. If the
// synthetic binding site ever smeared whole-object taint (a result port, a
// default leaf), this would light up.
func (i *Implementation) HandleUncaptured(ctx context.Context, req *pb.ClosureRequest) error {
	s := store.Notes()
	_ = req.GetQuery() // tainted, captured by nothing
	return store.WithTx(func(tx *store.Tx) error {
		return (&store.MaskDB{}).Selectx(s)
	})
}
