package app

import (
	"context"

	"example.com/backend/pb"
	"example.com/backend/store"
)

type Implementation struct {
	pb.UnimplementedAccountServer
	storage *store.Storage
	ledger  pb.LedgerClient
}

func New() *Implementation { return &Implementation{storage: &store.Storage{}} }

// GetAccount: req.GetNumber() (SOURCE) → FindAccountByNumber → Selectx (SINK).
func (i *Implementation) GetAccount(ctx context.Context, req *pb.GetAccountRequest) (*pb.AccountResponse, error) {
	acc, err := i.storage.FindAccountByNumber(req.GetNumber())
	if err != nil {
		return nil, err
	}
	return &pb.AccountResponse{Account: acc}, nil
}

// Archive (doc 17 Shape A middle hop): NO local sink — req.GetEntry() is
// forwarded over gRPC to the downstream ledger, whose handler holds the SQL
// sink. This handler is summarized before the ledger contract view exists
// (default leaf), so only the cross-repo fixpoint folds the remote sink into
// its summary.
func (i *Implementation) Archive(ctx context.Context, req *pb.ArchiveRequest) (*pb.ArchiveResponse, error) {
	if _, err := i.ledger.Record(ctx, &pb.RecordRequest{Entry: req.GetEntry()}); err != nil {
		return nil, err
	}
	return &pb.ArchiveResponse{Ok: true}, nil
}
