package pb

import "context"

type ErrRequest struct {
	Query string
	Name  string
}

func (r *ErrRequest) GetQuery() string { return r.Query }
func (r *ErrRequest) GetName() string  { return r.Name }

// ErrServer is the generated gRPC server interface. Handlers seed structurally
// (request params = source_params) — emit.go's requestParamIdxs needs the
// package path to contain "/pb", and the Implementation must embed
// Unimplemented<Svc>Server, or source_seeds is empty and the fixture proves
// nothing (HANDOFF trap 3).
type ErrServer interface {
	HandleErrOnly(context.Context, *ErrRequest) error
	HandleValueSurvives(context.Context, *ErrRequest) error
	HandleSingleErr(context.Context, *ErrRequest) error
	HandleWrapped(context.Context, *ErrRequest) error
	HandleTieBreak(context.Context, *ErrRequest) error
	HandleViaHelper(context.Context, *ErrRequest) error
	HandleHelperWraps(context.Context, *ErrRequest) error
}

type UnimplementedErrServer struct{}

func (UnimplementedErrServer) HandleErrOnly(context.Context, *ErrRequest) error       { return nil }
func (UnimplementedErrServer) HandleValueSurvives(context.Context, *ErrRequest) error { return nil }
func (UnimplementedErrServer) HandleSingleErr(context.Context, *ErrRequest) error     { return nil }
func (UnimplementedErrServer) HandleWrapped(context.Context, *ErrRequest) error       { return nil }
func (UnimplementedErrServer) HandleTieBreak(context.Context, *ErrRequest) error      { return nil }
func (UnimplementedErrServer) HandleViaHelper(context.Context, *ErrRequest) error     { return nil }
func (UnimplementedErrServer) HandleHelperWraps(context.Context, *ErrRequest) error   { return nil }
