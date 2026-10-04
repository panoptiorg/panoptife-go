// Package pb is a stand-in for protoc-gen-go-grpc output: a <Svc>Client
// interface and its message types, with NO server half. The module therefore
// has zero contracts — its only boundary is the client call site in app —
// which is exactly the shape the "no contracts" guard must distinguish from a
// repo whose pb layout the --pb-paths heuristic simply does not match.
package pb

import "context"

type SearchRequest struct{ Query string }

func (r *SearchRequest) GetQuery() string { return r.Query }

type SearchResponse struct{ Hits int32 }

type SearchClient interface {
	Search(ctx context.Context, in *SearchRequest) (*SearchResponse, error)
}
