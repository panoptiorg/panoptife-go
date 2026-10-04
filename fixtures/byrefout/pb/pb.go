package pb

import "context"

type ByRefRequest struct {
	Query  string
	MaskEq string
	Note   string
}

func (r *ByRefRequest) GetQuery() string  { return r.Query }
func (r *ByRefRequest) GetMaskEq() string { return r.MaskEq }
func (r *ByRefRequest) GetNote() string   { return r.Note }

// Handlers seed structurally (request params = source_params) only on a method
// of this interface, on a type embedding UnimplementedByRefServer, in a package
// whose path contains "/pb" (HANDOFF trap 3). Anything else has empty
// source_seeds and proves nothing.
type ByRefServer interface {
	HandleOutParam(context.Context, *ByRefRequest) error
	HandleReceiver(context.Context, *ByRefRequest) error
	HandlePassThrough(context.Context, *ByRefRequest) error
	HandleTwoOuts(context.Context, *ByRefRequest) error
	HandleUnread(context.Context, *ByRefRequest) error
}

type UnimplementedByRefServer struct{}

func (UnimplementedByRefServer) HandleOutParam(context.Context, *ByRefRequest) error    { return nil }
func (UnimplementedByRefServer) HandleReceiver(context.Context, *ByRefRequest) error    { return nil }
func (UnimplementedByRefServer) HandlePassThrough(context.Context, *ByRefRequest) error { return nil }
func (UnimplementedByRefServer) HandleTwoOuts(context.Context, *ByRefRequest) error     { return nil }
func (UnimplementedByRefServer) HandleUnread(context.Context, *ByRefRequest) error      { return nil }
