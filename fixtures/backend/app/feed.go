package app

import (
	"example.com/backend/pb"
	"example.com/backend/store"
)

// FeedImpl embeds the Unsafe escape hatch (not Unimplemented*) — detection
// caveat doc 16 §3.1 — and implements both streaming shapes (§3.2).
type FeedImpl struct {
	pb.UnsafeFeedServer
	storage *store.Storage
}

func NewFeed() *FeedImpl { return &FeedImpl{storage: &store.Storage{}} }

// Download (server-stream): req.GetNumber() (SOURCE) → Selectx (SINK), and the
// tainted row leaves over the stream (contract StreamOut → client Recv).
func (f *FeedImpl) Download(req *pb.DownloadRequest, s pb.Feed_DownloadServer) error {
	row, err := f.storage.FindAccountByNumber(req.GetNumber())
	if err != nil {
		return err
	}
	return s.Send(&pb.DownloadResponse{Data: row})
}

// Upload (client-stream): stream.Recv() (SOURCE / contract StreamIn) → Selectx.
func (f *FeedImpl) Upload(s pb.Feed_UploadServer) error {
	m, err := s.Recv()
	if err != nil {
		return err
	}
	if _, err := f.storage.Selectx(m.GetQuery()); err != nil {
		return err
	}
	return s.SendAndClose(&pb.UploadResponse{})
}
