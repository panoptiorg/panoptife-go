package pb

import "context"

// SearchRequest has two fields with disjoint sink destinations downstream:
// MaskEq feeds SQL, Note feeds exec. Field paths (doc 20 §2) must keep a
// taint on one field from firing the other field's sink.
type SearchRequest struct {
	MaskEq string
	Note   string
}

// Trivial generated-style getters — canonicalized to field projections by the
// frontend (verified against the SSA body, so these must stay trivial).
func (r *SearchRequest) GetMaskEq() string { return r.MaskEq }
func (r *SearchRequest) GetNote() string   { return r.Note }

type SearchResponse struct{ Rows string }

// SearchServer is the generated gRPC server interface.
type SearchServer interface {
	Search(context.Context, *SearchRequest) (*SearchResponse, error)
	Lookup(context.Context, *SearchRequest) (*SearchResponse, error)
	Export(context.Context, *SearchRequest) (*SearchResponse, error)
	ViaMask(context.Context, *SearchRequest) (*SearchResponse, error)
	ViaNote(context.Context, *SearchRequest) (*SearchResponse, error)
}

// UnimplementedSearchServer is embedded by server implementations.
type UnimplementedSearchServer struct{}

func (UnimplementedSearchServer) Search(context.Context, *SearchRequest) (*SearchResponse, error) {
	return nil, nil
}

func (UnimplementedSearchServer) Lookup(context.Context, *SearchRequest) (*SearchResponse, error) {
	return nil, nil
}

func (UnimplementedSearchServer) Export(context.Context, *SearchRequest) (*SearchResponse, error) {
	return nil, nil
}

func (UnimplementedSearchServer) ViaMask(context.Context, *SearchRequest) (*SearchResponse, error) {
	return nil, nil
}

func (UnimplementedSearchServer) ViaNote(context.Context, *SearchRequest) (*SearchResponse, error) {
	return nil, nil
}
