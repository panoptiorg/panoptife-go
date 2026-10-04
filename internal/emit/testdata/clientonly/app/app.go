package app

import (
	"context"

	"example.com/clientonly/pb"
)

type Svc struct{ cli pb.SearchClient }

func New(c pb.SearchClient) *Svc { return &Svc{cli: c} }

// Find is the only boundary in this module: an INVOKES_REMOTE call site,
// recognised only while pb's import path matches --pb-paths.
func (s *Svc) Find(ctx context.Context, q string) (int32, error) {
	resp, err := s.cli.Search(ctx, &pb.SearchRequest{Query: q})
	if err != nil {
		return 0, err
	}
	return resp.Hits, nil
}
