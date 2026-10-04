// Package pb fakes protoc-gen-go-grpc output: an Echo service (unary, with the
// Unimplemented embed) and a Feed service (streaming, with the Unsafe escape
// hatch) covering all three generated stream shapes.
package pb

import "context"

type EchoRequest struct{ Query string }

func (r *EchoRequest) GetQuery() string { return r.Query }

type EchoResponse struct{ Msg string }

type EchoServer interface {
	Get(context.Context, *EchoRequest) (*EchoResponse, error)
}

type UnimplementedEchoServer struct{}

func (UnimplementedEchoServer) Get(context.Context, *EchoRequest) (*EchoResponse, error) {
	return nil, nil
}

// UnsafeFeedServer is the generated opt-out from forward compatibility.
type UnsafeFeedServer interface {
	mustEmbedUnimplementedFeedServer()
}

type DownloadRequest struct{ Number string }

func (r *DownloadRequest) GetNumber() string { return r.Number }

type DownloadResponse struct{ Data string }

func (r *DownloadResponse) GetData() string { return r.Data }

type UploadRequest struct{ Query string }

func (r *UploadRequest) GetQuery() string { return r.Query }

type UploadResponse struct{}

type ChatMessage struct{ Body string }

func (m *ChatMessage) GetBody() string { return m.Body }

// Server-stream: Send only.
type Feed_DownloadServer interface {
	Send(*DownloadResponse) error
}

// Client-stream: Recv + SendAndClose.
type Feed_UploadServer interface {
	Recv() (*UploadRequest, error)
	SendAndClose(*UploadResponse) error
}

// Bidi: Send + Recv.
type Feed_ChatServer interface {
	Send(*ChatMessage) error
	Recv() (*ChatMessage, error)
}

// Used by the Bad negative (two results) in app.
type Feed_BadServer interface {
	Recv() (*UploadRequest, error)
}

type FeedClient interface {
	Download(ctx context.Context, in *DownloadRequest) (Feed_DownloadClient, error)
	Upload(ctx context.Context) (Feed_UploadClient, error)
	Chat(ctx context.Context) (Feed_ChatClient, error)
}

type Feed_DownloadClient interface {
	Recv() (*DownloadResponse, error)
	CloseSend() error
}

type Feed_UploadClient interface {
	Send(*UploadRequest) error
	CloseAndRecv() (*UploadResponse, error)
}

type Feed_ChatClient interface {
	Send(*ChatMessage) error
	Recv() (*ChatMessage, error)
}
