package pb

import "context"

type GetAccountRequest struct{ Number string }

func (r *GetAccountRequest) GetNumber() string { return r.Number }

type AccountResponse struct{ Account string }

type ArchiveRequest struct{ Entry string }

func (r *ArchiveRequest) GetEntry() string { return r.Entry }

type ArchiveResponse struct{ Ok bool }

// AccountServer is the generated gRPC server interface.
type AccountServer interface {
	GetAccount(context.Context, *GetAccountRequest) (*AccountResponse, error)
	Archive(context.Context, *ArchiveRequest) (*ArchiveResponse, error)
}

// UnimplementedAccountServer is embedded by server implementations.
type UnimplementedAccountServer struct{}

func (UnimplementedAccountServer) GetAccount(context.Context, *GetAccountRequest) (*AccountResponse, error) {
	return nil, nil
}

func (UnimplementedAccountServer) Archive(context.Context, *ArchiveRequest) (*ArchiveResponse, error) {
	return nil, nil
}

// AccountClient is the generated gRPC client interface.
type AccountClient interface {
	GetAccount(ctx context.Context, in *GetAccountRequest) (*AccountResponse, error)
	Archive(ctx context.Context, in *ArchiveRequest) (*ArchiveResponse, error)
}

// --- Ledger: vendored client types for the downstream ledger service ---

type RecordRequest struct{ Entry string }

type RecordResponse struct{ Ok bool }

// LedgerClient is the generated gRPC client interface (vendored copy).
type LedgerClient interface {
	Record(ctx context.Context, in *RecordRequest) (*RecordResponse, error)
}

// --- Feed: streaming service, server declared via the Unsafe escape hatch ---

type DownloadRequest struct{ Number string }

func (r *DownloadRequest) GetNumber() string { return r.Number }

type DownloadResponse struct{ Data string }

func (r *DownloadResponse) GetData() string { return r.Data }

type UploadRequest struct{ Query string }

func (r *UploadRequest) GetQuery() string { return r.Query }

type UploadResponse struct{}

// UnsafeFeedServer is the generated forward-compatibility opt-out.
type UnsafeFeedServer interface {
	mustEmbedUnimplementedFeedServer()
}

// Feed_DownloadServer is the generated server-stream interface.
type Feed_DownloadServer interface {
	Send(*DownloadResponse) error
}

// Feed_UploadServer is the generated client-stream interface.
type Feed_UploadServer interface {
	Recv() (*UploadRequest, error)
	SendAndClose(*UploadResponse) error
}
