package pb

import "context"

type QueryRequest struct{ Query string }

func (r *QueryRequest) GetQuery() string { return r.Query }

// QueryServer is the generated gRPC server interface — handlers seed
// structurally (request params = source_params), the same convention the
// real corpus uses; the catalog's `Request).Get*` sourcing is inert once
// trivial getters are canonicalized to projections (doc 20 §2.2.1).
type QueryServer interface {
	HandleNarrow(context.Context, *QueryRequest) error
	HandleWide(context.Context, *QueryRequest) error
}

// UnimplementedQueryServer is embedded by server implementations.
type UnimplementedQueryServer struct{}

func (UnimplementedQueryServer) HandleNarrow(context.Context, *QueryRequest) error { return nil }
func (UnimplementedQueryServer) HandleWide(context.Context, *QueryRequest) error   { return nil }
