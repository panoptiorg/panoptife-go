package app

import (
	"context"
	"encoding/json"

	"example.com/fieldpath/pb"
	"example.com/fieldpath/store"
)

type Implementation struct {
	pb.UnimplementedSearchServer
	storage *store.Storage
}

func New() *Implementation { return &Implementation{storage: &store.Storage{}} }

// Search: whole request (structural gRPC source) flows into Filter — the
// positive control: BOTH sinks fire (whole-tainted req covers both fields).
func (i *Implementation) Search(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	rows, err := i.storage.Filter(req)
	if err != nil {
		return nil, err
	}
	return &pb.SearchResponse{Rows: rows}, nil
}

// Lookup: only MaskEq carries tainted data (Note is a constant). Pre-R6 the
// whole-struct smear fired the exec sink too — the FP field paths kill:
// assert (Lookup, sqli) present, (Lookup, exec) ABSENT.
func (i *Implementation) Lookup(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	q := &pb.SearchRequest{Note: "audit"}
	q.MaskEq = req.GetMaskEq()
	rows, err := i.storage.Filter(q)
	if err != nil {
		return nil, err
	}
	return &pb.SearchResponse{Rows: rows}, nil
}

// ViaMask / ViaNote: Lookup with one extra frame. The tainted struct passes
// through store.PassThrough, which reads no field of it, so the k=2 path is
// lost there (doc 25 R1). Both reach the SAME pair of same-class sinks in
// FilterTwo, from OPPOSITE fields — so whichever way the descent's tie-break
// sorts, exactly one of the two routes is wrong today. That is what makes this
// fixture tie-break-independent rather than an assertion on sort order.
//
// Chain-wise both are true positives; the defect being asserted is the ROUTE.
func (i *Implementation) ViaMask(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	q := &pb.SearchRequest{Note: "audit"}
	q.MaskEq = req.GetMaskEq()
	rows, err := i.storage.PassThrough(q)
	if err != nil {
		return nil, err
	}
	return &pb.SearchResponse{Rows: rows}, nil
}

func (i *Implementation) ViaNote(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	q := &pb.SearchRequest{MaskEq: "mask"}
	q.Note = req.GetNote()
	rows, err := i.storage.PassThrough(q)
	if err != nil {
		return nil, err
	}
	return &pb.SearchResponse{Rows: rows}, nil
}

// Export: the field-tainted struct goes through an OUT-OF-SCOPE callee
// (json.Marshal → default leaf) into the SQL sink — the whole-object escape
// hatch (doc 20 §2.2.6): the chain MUST survive (soundness guard).
func (i *Implementation) Export(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	q := &pb.SearchRequest{Note: "x"}
	q.MaskEq = req.GetMaskEq()
	b, _ := json.Marshal(q)
	rows, err := i.storage.Selectx(string(b))
	if err != nil {
		return nil, err
	}
	return &pb.SearchResponse{Rows: rows}, nil
}
