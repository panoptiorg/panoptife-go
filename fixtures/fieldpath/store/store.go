package store

import (
	"os/exec"

	"example.com/fieldpath/pb"
)

type Storage struct{}

// Selectx models a pgx-wrapper SQL exec sink (catalog class "sqli").
func (s *Storage) Selectx(query string) (string, error) {
	return "row:" + query, nil
}

// Filter reads BOTH request fields into DISJOINT sinks — one via a
// canonicalized getter, one via direct field access. Its summary must carry
// two field-keyed rows, never a whole-object row:
//   (param_0[MaskEq] → sqli)   (param_0[Note] → exec)
func (s *Storage) Filter(q *pb.SearchRequest) (string, error) {
	cmd := exec.Command("audit-log", q.Note) // exec SINK — Note only
	_ = cmd
	return s.Selectx(q.GetMaskEq()) // sqli SINK — MaskEq only
}

// PassThrough hands q to FilterTwo without reading ANY field of it, so its only
// param_0 in-slot is the whole-object one — emitAccessVariants builds the
// vocabulary from locally projected paths only (flow.go). This is where a
// caller's k=2 path is lost: the whole-object row is prefix-comparable with the
// caller's [MaskEq] fact, fires, and the residual is dropped.
//
// It is the fixture for doc 25 R1, the widening the real corpus hits at
// documents.go:245 -> :606 -> :861.
func (s *Storage) PassThrough(q *pb.SearchRequest) (string, error) {
	return s.FilterTwo(q)
}

// FilterTwo has two sinks of the SAME class on DISJOINT fields. A fact that
// arrived widened to whole-object is prefix-comparable with both in-slots, so
// both fire and the route descends whichever one the tie-break happens to sort
// first — a branch the call path never enters.
func (s *Storage) FilterTwo(q *pb.SearchRequest) (string, error) {
	if q.Note != "" {
		return s.Selectx("note = " + q.Note) // sqli SINK — Note only
	}
	return s.Selectx("mask = " + q.GetMaskEq()) // sqli SINK — MaskEq only
}
