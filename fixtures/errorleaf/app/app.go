// Package app is the B1 / doc 31 acceptance fixture: an `error`-typed result of
// a call with no resolvable summary is treated as taint-transparent, the error
// is logged, and the reported field is not in the message. doc 30 §6.2 measured
// that class at 214-278 keys (~25% of the corpus), the largest anywhere in this
// tree.
//
// Every case pairs a suppression with the recall guard that must survive it.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"example.com/errorleaf/pb"
	"example.com/errorleaf/store"
	"example.com/errorleaf/internal/logger"
)

type Implementation struct {
	pb.UnimplementedErrServer
	mask *store.MaskDB
	note *store.NoteDB
}

// HandleErrOnly — THE false positive. json.Marshal is out of scope, so it is a
// default leaf; its error result is tainted because its argument is, and only
// the error is logged. Present with the flag off, gone with it on.
func (i *Implementation) HandleErrOnly(ctx context.Context, req *pb.ErrRequest) error {
	_, err := json.Marshal(req.GetQuery())
	if err != nil {
		logger.Errorf("marshal failed: %v", err)
	}
	return nil
}

// HandleValueSurvives — the SAME leaf call, but the VALUE result is logged. The
// rule is per result port, not per callsite, so this must survive.
func (i *Implementation) HandleValueSurvives(ctx context.Context, req *pb.ErrRequest) error {
	b, err := json.Marshal(req.GetQuery())
	_ = err
	logger.Errorf("marshalled %s", string(b))
	return nil
}

// HandleSingleErr — a producer whose ONLY result is the error (json.Unmarshal).
// This is the population the rejected "error is the last of N results" proxy
// could not see, and it carries most of the real keys (doc 31 §3a: 156 of 166
// on ledger).
func (i *Implementation) HandleSingleErr(ctx context.Context, req *pb.ErrRequest) error {
	var v map[string]string
	if err := json.Unmarshal([]byte(req.GetQuery()), &v); err != nil {
		logger.Errorf("unmarshal: %v", err)
	}
	return nil
}

// HandleWrapped — doc 25 R3's trap. fmt.Errorf is itself an out-of-scope call
// returning one error, and it really does carry request data. The catalog's
// error_wrappers exempt it, so this chain must survive.
func (i *Implementation) HandleWrapped(ctx context.Context, req *pb.ErrRequest) error {
	_, err := strconv.Atoi(req.GetQuery())
	if err != nil {
		wrapped := fmt.Errorf("bad query for %s: %w", req.GetName(), err)
		logger.Errorf("%v", wrapped)
	}
	return nil
}

// HandleTieBreak — HANDOFF trap 4 in one frame: TWO same-class (sqli) sinks, one
// fed by the leaf's VALUE and one by `err.Error()`. Whichever way witness's
// candidate sort falls, the pre-fix tree reports a wrong route for one of them,
// so this case cannot pass vacuously.
func (i *Implementation) HandleTieBreak(ctx context.Context, req *pb.ErrRequest) error {
	n, err := strconv.Atoi(req.GetQuery())
	_ = i.mask.Selectx(strconv.Itoa(n))
	if err != nil {
		_ = i.note.Selectx(err.Error())
	}
	return nil
}

// HandleViaHelper — the producer is IN SCOPE (store.Load), so the core composes
// its summary rather than applying a leaf. The claim being tested: the rule
// still reaches it, because Load's error is only tainted if the leaf INSIDE Load
// tainted it. Gone with the flag on.
func (i *Implementation) HandleViaHelper(ctx context.Context, req *pb.ErrRequest) error {
	if _, err := store.Load(req.GetQuery()); err != nil {
		logger.Errorf("load: %v", err)
	}
	return nil
}

// HandleHelperWraps — the same shape, except the in-scope helper BUILDS its
// error out of the request. It must survive: this is the case a summary-side
// rule would have deleted, and it is why the rule lives at the leaf.
func (i *Implementation) HandleHelperWraps(ctx context.Context, req *pb.ErrRequest) error {
	if _, err := store.LoadWrapping(req.GetQuery()); err != nil {
		logger.Errorf("load: %v", err)
	}
	return nil
}
