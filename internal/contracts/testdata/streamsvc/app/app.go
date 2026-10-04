package app

import (
	"context"

	"example.com/streamsvc/other"
	"example.com/streamsvc/pb"
)

type EchoImpl struct {
	pb.UnimplementedEchoServer
}

func (e *EchoImpl) Get(ctx context.Context, req *pb.EchoRequest) (*pb.EchoResponse, error) {
	return &pb.EchoResponse{Msg: req.GetQuery()}, nil
}

type FeedImpl struct {
	pb.UnsafeFeedServer
}

func (f *FeedImpl) Download(req *pb.DownloadRequest, s pb.Feed_DownloadServer) error {
	return s.Send(&pb.DownloadResponse{Data: req.GetNumber()})
}

func (f *FeedImpl) Upload(s pb.Feed_UploadServer) error {
	m, err := s.Recv()
	if err != nil {
		return err
	}
	_ = m.GetQuery()
	return s.SendAndClose(&pb.UploadResponse{})
}

func (f *FeedImpl) Chat(s pb.Feed_ChatServer) error {
	m, err := s.Recv()
	if err != nil {
		return err
	}
	return s.Send(m)
}

// Negatives — none of these may be extracted as handlers.

// §3.5 phantoms: exported ctx-first helpers on a server struct whose pb
// package HAS the generated EchoServer interface — the RPC-set membership
// check must reject them (shape alone would classify both as unary).

// validation-helper shape: (ctx, req) error
func (e *EchoImpl) GetRequestValidate(ctx context.Context, req *pb.EchoRequest) error {
	return nil
}

// feature-flag-helper shape: (ctx, scalar) (bool, error)
func (e *EchoImpl) IsFeatureEnabled(ctx context.Context, id int64) (bool, error) {
	return false, nil
}

// no ctx, no stream param
func (f *FeedImpl) Helper(x string) string { return x }

// stream-shaped iface from a non-pb package
func (f *FeedImpl) Evil(s other.Feed_EvilServer) error { return nil }

// two results
func (f *FeedImpl) Bad(s pb.Feed_BadServer) (int, error) { return 0, nil }

// stream type name doesn't match the method name
func (f *FeedImpl) Mismatch(s pb.Feed_DownloadServer) error { return nil }
