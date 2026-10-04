package pb

import "context"

type WrapRequest struct{ Query string }

func (r *WrapRequest) GetQuery() string { return r.Query }

// WrapServer is the generated gRPC server interface. Handlers seed
// structurally (request params = source_params) — see emit.go requestParamIdxs,
// which needs the package path to contain "/pb". A fixture case that is not a
// handler on this interface produces no source_seeds and proves nothing.
type WrapServer interface {
	HandleBound(context.Context, *WrapRequest) error
	HandleThunk(context.Context, *WrapRequest) error
	HandlePromoted(context.Context, *WrapRequest) error
	HandleOutOfScope(context.Context, *WrapRequest) error
}

type UnimplementedWrapServer struct{}

func (UnimplementedWrapServer) HandleBound(context.Context, *WrapRequest) error      { return nil }
func (UnimplementedWrapServer) HandleThunk(context.Context, *WrapRequest) error      { return nil }
func (UnimplementedWrapServer) HandlePromoted(context.Context, *WrapRequest) error   { return nil }
func (UnimplementedWrapServer) HandleOutOfScope(context.Context, *WrapRequest) error { return nil }
