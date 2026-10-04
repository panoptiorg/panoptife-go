package app

import (
	"context"
	"os/exec"

	"example.com/heapslots/pb"
	"example.com/heapslots/pool"
	"example.com/heapslots/store"
)

// Implementation embeds the generated Unimplemented server, which is what makes
// requestParamIdxs fill source_params (HANDOFF trap 3).
type Implementation struct {
	pb.UnimplementedHeapServer

	mask  *store.MaskDB
	note  *store.NoteDB
	batch *pool.Pool
	twoF  *pool.Pool
}

func New() *Implementation {
	i := &Implementation{mask: &store.MaskDB{}, note: &store.NoteDB{}}
	// the handler is a method VALUE => a $bound wrapper whose single free var is
	// the captured receiver (W1d), stored in a struct field and only ever called
	// from runWorker.
	i.batch = pool.New(i.runBatch)
	i.twoF = pool.New(i.runTwoFields)
	return i
}

// runBatch is the consumer end: it is never called from any handler, only from
// runWorker via the stored handler field.
func (i *Implementation) runBatch(items []string) error {
	for _, it := range items {
		if err := i.mask.Selectx(it); err != nil {
			return err
		}
	}
	return nil
}

// HandlePlainSend: source -> bare `ch <- v` -> ... -> MaskDB.Selectx.
func (i *Implementation) HandlePlainSend(ctx context.Context, req *pb.HeapRequest) error {
	i.batch.Submit(req.GetQuery())
	return nil
}

// HandleSelectSend: the same, with the send inside a `select` — the shape 77%
// of ledger-svc's field-sends actually use.
func (i *Implementation) HandleSelectSend(ctx context.Context, req *pb.HeapRequest) error {
	i.batch.SubmitSelect(req.GetQuery())
	return nil
}

// HandleCopyHop exercises the `copy` builtin on its own, with no channel and no
// goroutine involved, so a failure here is unambiguous: the copy arm, nothing
// else. dst is a field of a heap object read back by readCopied.
type buf struct{ items []string }

var shared = &buf{}

func readCopied() error {
	// reads the cell `buf.items` — no caller relationship with HandleCopyHop
	for _, s := range shared.items {
		if err := (&store.MaskDB{}).Selectx(s); err != nil {
			return err
		}
	}
	return nil
}

func (i *Implementation) HandleCopyHop(ctx context.Context, req *pb.HeapRequest) error {
	src := []string{req.GetQuery()}
	dst := make([]string, len(src))
	copy(dst, src)
	shared.items = dst
	return nil
}

// HandleTwoFields is the tie-break-independent guard (HANDOFF trap 4). ONE
// struct, TWO fields, TWO same-class sinks, and only `Eq` is tainted: whichever
// way witness's candidate sort falls, routing the fact onto `Note` is wrong.
// A cell keyed on the TYPE alone rather than (type, field) passes the recall
// assertion and fails this one.
type twoCell struct {
	Eq   string
	Note string
}

var two = &twoCell{}

func readEq() error   { return (&store.MaskDB{}).Selectx(two.Eq) }
func readNote() error { return (&store.NoteDB{}).Selectx(two.Note) }

func (i *Implementation) runTwoFields(items []string) error {
	if len(items) > 0 {
		two.Eq = items[0]
	}
	return nil
}

func (i *Implementation) HandleTwoFields(ctx context.Context, req *pb.HeapRequest) error {
	two.Eq = req.GetMaskEq()
	two.Note = store.Notes() // untainted
	return nil
}

// HandleUnread is the precision negative: it writes a cell that NOTHING reads,
// so no chain may appear. If cells smeared at object rather than field
// granularity this would light up through `two.Eq`'s reader.
type unread struct{ Spare string }

var never = &unread{}

func (i *Implementation) HandleUnread(ctx context.Context, req *pb.HeapRequest) error {
	never.Spare = req.GetNote()
	return nil
}

// ---------------------------------------------------------------------------
// A16 — interface-typed cells (doc 30 §6.1, debt A16)
//
// The ledger shape in miniature: `Evt.Src` is INTERFACE-typed, written with a
// different concrete type on each of two mutually exclusive paths, and read in
// ONE place behind `.(SBPParam)`. The cell pairs every writer with that reader;
// exactly one pairing can ever have happened.
//
// The narrowing A16 implements is the TYPE ASSERTION, not arm exclusivity — it
// cannot see that `Kind` selects the arm. So the two writes live in two
// functions here, which is also what makes the assertion expressible: with the
// narrowing on, HandleDFAArm must lose its chain and HandleSBPArm must keep its.
// ---------------------------------------------------------------------------

type SBPParam string
type DFAParam string

type Evt struct {
	Kind  int
	Src   any // read ONLY behind a type assertion  => narrowable
	Meta  any // read raw, no assertion             => must NOT be narrowed
	Mixed any // read BOTH ways in one function     => per-read-site granularity
}

var evt = &Evt{}

func writeSBP(v string) { evt.Src = SBPParam(v) }
func writeDFA(v string) { evt.Src = DFAParam(v) }

// readEvt is the discriminating reader: it can only ever have seen the SBPParam
// write. No caller relationship with either writer — the join is the cell's.
func readEvt() error {
	p, ok := evt.Src.(SBPParam)
	if !ok {
		return nil
	}
	return (&store.MaskDB{}).Selectx(string(p))
}

func (i *Implementation) HandleSBPArm(ctx context.Context, req *pb.HeapRequest) error {
	writeSBP(req.GetQuery())
	return nil
}

func (i *Implementation) HandleDFAArm(ctx context.Context, req *pb.HeapRequest) error {
	writeDFA(req.GetQuery())
	return nil
}

// HandleRawIface is the RECALL guard, and the reason doc 30 §7b rejected the
// cheap "never open an interface-typed cell" filter: `Evt.Meta` is interface
// typed and its reader does not discriminate, so every writer really can be the
// value it sees. This chain must survive the narrowing untouched.
func writeMeta(v string) { evt.Meta = DFAParam(v) }

func readMeta() error { return (&store.NoteDB{}).Getx(evt.Meta) }

func (i *Implementation) HandleRawIface(ctx context.Context, req *pb.HeapRequest) error {
	writeMeta(req.GetQuery())
	return nil
}

// readMixed reads ONE cell twice: once through the assertion and once raw, the
// exact shape of ledger's `documents.go:486-489` (the `!ok` branch logs
// `event.EventSrc` itself). x/tools SSA emits a separate load per occurrence, so
// the two reads are different values and only the guarded one is constrained.
// A function-level narrowing would get this wrong in one direction or the other.
//
// The two reads land on sinks of DIFFERENT classes on purpose: same-class
// chains from one source collapse in the N12 dedupe, so `exec` vs `sqli` is
// what makes "one of the two survived" observable at all.
func writeMixedSBP(v string) { evt.Mixed = SBPParam(v) }
func writeMixedDFA(v string) { evt.Mixed = DFAParam(v) }

func readMixed() error {
	p, ok := evt.Mixed.(SBPParam)
	if !ok {
		return (&store.NoteDB{}).Getx(evt.Mixed) // raw [sqli]: accepts any writer
	}
	return exec.Command(string(p)).Run() // guarded [exec]: SBPParam only
}

func (i *Implementation) HandleMixedSBP(ctx context.Context, req *pb.HeapRequest) error {
	writeMixedSBP(req.GetQuery())
	return nil
}

func (i *Implementation) HandleMixedDFA(ctx context.Context, req *pb.HeapRequest) error {
	writeMixedDFA(req.GetQuery())
	return nil
}

// keep the readers reachable for the loader (they are deliberately not called
// from any handler — that is the point).
var _ = []func() error{readCopied, readEq, readNote, readEvt, readMeta, readMixed}
