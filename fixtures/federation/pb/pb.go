package pb

import "context"

type GetAccountRequest struct{ Number string }
type AccountResponse struct{ Account string }

type ArchiveRequest struct{ Entry string }
type ArchiveResponse struct{ Ok bool }

// AccountClient is the generated gRPC client interface (vendored copy).
type AccountClient interface {
	GetAccount(ctx context.Context, in *GetAccountRequest) (*AccountResponse, error)
	Archive(ctx context.Context, in *ArchiveRequest) (*ArchiveResponse, error)
}

// --- Feed: vendored streaming client types ---

type DownloadRequest struct{ Number string }

type DownloadResponse struct{ Data string }

func (r *DownloadResponse) GetData() string { return r.Data }

type UploadRequest struct{ Query string }

type UploadResponse struct{}

type FeedClient interface {
	Download(ctx context.Context, in *DownloadRequest) (Feed_DownloadClient, error)
	Upload(ctx context.Context) (Feed_UploadClient, error)
}

type Feed_DownloadClient interface {
	Recv() (*DownloadResponse, error)
	CloseSend() error
}

type Feed_UploadClient interface {
	Send(*UploadRequest) error
	CloseAndRecv() (*UploadResponse, error)
}
