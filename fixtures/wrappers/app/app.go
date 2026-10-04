// Package app exercises SYNTHETIC WRAPPER transparency (doc 24 N1 / WS-E W0).
//
// x/tools/go/ssa auto-generates package-less, Synthetic!="" functions for
// method values (`$bound`), method expressions (`$thunk`) and methods promoted
// from an embedded struct. Before W0, emit dropped all of them and the resolver
// refused them as dispatch targets, so a call through such a func value resolved
// to ZERO targets, went `Opaque`, fell back to the default leaf, and the entire
// callee body became invisible from that call site.
//
// Each handler below routes request data to the SQL sink through exactly one of
// those wrapper kinds, with no channel or goroutine in the way — unlike the real
// epool case (N5), which additionally needs heap-field slots and is NOT closed
// by W0.
package app

import (
	"context"
	"strings"

	"example.com/wrappers/pb"
	"example.com/wrappers/store"
)

type Implementation struct {
	pb.UnimplementedWrapServer
	storage *store.Storage
	// runner holds a METHOD VALUE: `i.query` compiles to
	// MakeClosure((*Implementation).query$bound, [i]).
	runner func(string) string
}

func New() *Implementation {
	i := &Implementation{storage: &store.Storage{}}
	i.runner = i.query // <- the $bound wrapper
	return i
}

// query is the wrapped method: it reaches the sink. Invisible from `runner`
// unless the $bound wrapper is emitted.
func (i *Implementation) query(q string) string {
	row, _ := i.storage.Selectx("SELECT * FROM t WHERE q = " + q)
	return row
}

// HandleBound: request -> stored method value -> query -> Selectx.
func (i *Implementation) HandleBound(ctx context.Context, req *pb.WrapRequest) error {
	_ = i.runner(req.GetQuery())
	return nil
}

// --- method expression (`$thunk`) ---

// thunked is a method EXPRESSION: (*Implementation).lookup has an explicit
// receiver param, which ssa implements with a $thunk wrapper.
var thunked = (*Implementation).lookup

func (i *Implementation) lookup(q string) string {
	row, _ := i.storage.Selectx("SELECT * FROM u WHERE q = " + q)
	return row
}

// HandleThunk: request -> method expression -> lookup -> Selectx.
func (i *Implementation) HandleThunk(ctx context.Context, req *pb.WrapRequest) error {
	_ = thunked(i, req.GetQuery())
	return nil
}

// --- embedded promotion wrapper ---

type base struct{ storage *store.Storage }

// Search is promoted onto Decorated; calling it through the Searcher interface
// dispatches to a synthetic promotion wrapper, not to base.Search directly.
func (b base) Search(q string) string {
	row, _ := b.storage.Selectx("SELECT * FROM v WHERE q = " + q)
	return row
}

type Decorated struct{ base }

type Searcher interface{ Search(string) string }

// HandlePromoted: request -> interface call -> promotion wrapper -> base.Search.
func (i *Implementation) HandlePromoted(ctx context.Context, req *pb.WrapRequest) error {
	var s Searcher = Decorated{base{storage: i.storage}}
	_ = s.Search(req.GetQuery())
	return nil
}

// --- negative control ---

// HandleOutOfScope routes the request through a method value on a STDLIB type.
// `strings.Builder` is out of scope, so declaringScopePkg rejects its wrapper
// and no chain may appear — W0 must stay scope-limited, not emit every wrapper
// in the program.
func (i *Implementation) HandleOutOfScope(ctx context.Context, req *pb.WrapRequest) error {
	var b strings.Builder
	write := b.WriteString // $bound on an out-of-scope type
	_, _ = write(req.GetQuery())
	return nil
}
