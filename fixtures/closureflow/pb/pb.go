package pb

import "context"

type ClosureRequest struct {
	Query  string
	MaskEq string
	Note   string
}

func (r *ClosureRequest) GetQuery() string  { return r.Query }
func (r *ClosureRequest) GetMaskEq() string { return r.MaskEq }
func (r *ClosureRequest) GetNote() string   { return r.Note }

// ClosureServer is the generated gRPC server interface. Handlers seed
// structurally (request params = source_params) — see emit.go requestParamIdxs,
// which needs the package path to contain "/pb". A fixture case that is not a
// handler on this interface produces no source_seeds and proves nothing
// (HANDOFF trap 3).
type ClosureServer interface {
	HandleTx(context.Context, *ClosureRequest) error
	HandleBoundRecv(context.Context, *ClosureRequest) error
	HandleTwoCaptures(context.Context, *ClosureRequest) error
	HandleUncaptured(context.Context, *ClosureRequest) error
}

type UnimplementedClosureServer struct{}

func (UnimplementedClosureServer) HandleTx(context.Context, *ClosureRequest) error          { return nil }
func (UnimplementedClosureServer) HandleBoundRecv(context.Context, *ClosureRequest) error   { return nil }
func (UnimplementedClosureServer) HandleTwoCaptures(context.Context, *ClosureRequest) error { return nil }
func (UnimplementedClosureServer) HandleUncaptured(context.Context, *ClosureRequest) error  { return nil }
