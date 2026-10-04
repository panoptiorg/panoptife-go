package client

import (
	"context"

	"example.com/streamsvc/pb"
)

// Run exercises the client-side stream ports: open calls, Send, Recv, and a
// non-port stream method (CloseSend).
func Run(ctx context.Context, c pb.FeedClient, q string) (string, error) {
	us, err := c.Upload(ctx)
	if err != nil {
		return "", err
	}
	if err := us.Send(&pb.UploadRequest{Query: q}); err != nil {
		return "", err
	}
	if _, err := us.CloseAndRecv(); err != nil {
		return "", err
	}

	ds, err := c.Download(ctx, &pb.DownloadRequest{Number: q})
	if err != nil {
		return "", err
	}
	m, err := ds.Recv()
	if err != nil {
		return "", err
	}
	if err := ds.CloseSend(); err != nil {
		return "", err
	}
	return m.GetData(), nil
}
