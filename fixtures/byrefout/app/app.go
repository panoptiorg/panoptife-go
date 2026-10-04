// Fixture for WS-E W1a (doc 29): by-ref param + receiver out-slots, and the
// caller-side arg back-edge that makes them carry.
//
// One positive per independently-breakable piece, so a regression in one cannot
// hide behind another:
//
//	HandleOutParam    OUT_PARAM_BYREF   — a plain `*T` out param
//	HandleReceiver    OUT_RECEIVER_BYREF — the receiver, which OUT_PARAM_BYREF
//	                  cannot address (doc 29 §1b): with the naive ByRefParam(0)
//	                  encoding the fact lands on the FIRST REAL ARGUMENT, so
//	                  this case pins the N3 bug arriving from the other side
//	HandlePassThrough the unconditional predicate — the middle frame writes
//	                  nothing locally, so a "written-through" predicate drops
//	                  the whole chain (doc 29 §1d)
//	HandleTwoOuts     tie-break independence (HANDOFF trap 4): ONE callee, TWO
//	                  by-ref outs, two same-class sinks, only one tainted
//	HandleUnread      precision negative: an out param nobody reads back
package app

import (
	"context"

	"example.com/byrefout/pb"
	"example.com/byrefout/store"
)

type Implementation struct {
	pb.UnimplementedByRefServer

	mask *store.MaskDB
	note *store.NoteDB
}

func New() *Implementation {
	return &Implementation{mask: &store.MaskDB{}, note: &store.NoteDB{}}
}

// ---- 1. by-ref OUT PARAM -----------------------------------------------

type Query struct {
	Where string
	Order string
}

// fillWhere writes through its `dst` param and returns nothing the caller can
// use. Without an OUT_PARAM_BYREF out-slot its entire effect is invisible.
func fillWhere(dst *Query, v string) { dst.Where = v }

func (i *Implementation) HandleOutParam(ctx context.Context, req *pb.ByRefRequest) error {
	var q Query
	fillWhere(&q, req.GetQuery())
	return i.mask.Selectx(q.Where)
}

// ---- 2. by-ref RECEIVER ------------------------------------------------

type Builder struct {
	SQL  string
	Note string
}

// SetSQL mutates its RECEIVER from an argument. Encoded as ByRefParam(0) the
// fact lands on `v` (the caller's first real argument) instead of on `b`.
func (b *Builder) SetSQL(v string) { b.SQL = v }

func (i *Implementation) HandleReceiver(ctx context.Context, req *pb.ByRefRequest) error {
	var b Builder
	b.SetSQL(req.GetQuery())
	return i.mask.Selectx(b.SQL)
}

// ---- 3. PASS-THROUGH ---------------------------------------------------

// relay writes nothing locally — it only hands `dst` on. A predicate that
// emitted out-slots solely for locally-written params would give `relay` none
// and break the chain here, which is why W1a emits unconditionally.
func relay(dst *Query, v string) { fillWhere(dst, v) }

func (i *Implementation) HandlePassThrough(ctx context.Context, req *pb.ByRefRequest) error {
	var q Query
	relay(&q, req.GetQuery())
	return i.mask.Selectx(q.Where)
}

// ---- 4. TWO OUT PARAMS, ONE CALL ---------------------------------------

// split writes each of its two by-ref params from a DIFFERENT source. Only
// `eq` is tainted, so routing the fact onto `note` is wrong no matter which way
// witness's candidate sort falls (HANDOFF trap 4). An out-slot keyed on the
// call rather than on the param index passes every recall assertion and fails
// this one.
func split(eq *Query, note *Query, tainted, clean string) {
	eq.Where = tainted
	note.Where = clean
}

func (i *Implementation) HandleTwoOuts(ctx context.Context, req *pb.ByRefRequest) error {
	var eq, note Query
	split(&eq, &note, req.GetMaskEq(), store.Notes())
	if err := i.mask.Selectx(eq.Where); err != nil {
		return err
	}
	return i.note.Selectx(note.Where)
}

// ---- 5. precision negative ---------------------------------------------

// HandleUnread writes a by-ref param that nothing ever reads back. No chain may
// appear: an out-slot must carry a fact to a sink, not merely exist.
func (i *Implementation) HandleUnread(ctx context.Context, req *pb.ByRefRequest) error {
	var spare Query
	fillWhere(&spare, req.GetNote())
	return nil
}
