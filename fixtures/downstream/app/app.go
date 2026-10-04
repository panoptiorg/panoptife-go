package app

import (
	"context"

	"example.com/downstream/pb"
	"example.com/downstream/store"
)

type LedgerImpl struct {
	pb.UnimplementedLedgerServer
	storage *store.Storage
}

func New() *LedgerImpl { return &LedgerImpl{storage: &store.Storage{}} }

// Record: req.GetEntry() (SOURCE) → Selectx (SINK) — the terminal repo of the
// two-boundary chain federation → backend → downstream (doc 17 Shape A).
func (l *LedgerImpl) Record(ctx context.Context, req *pb.RecordRequest) (*pb.RecordResponse, error) {
	if _, err := l.storage.Selectx(req.GetEntry()); err != nil {
		return nil, err
	}
	return &pb.RecordResponse{Ok: true}, nil
}
