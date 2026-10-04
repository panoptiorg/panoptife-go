package pb

import "context"

type HeapRequest struct {
	Query  string
	MaskEq string
	Note   string
}

func (r *HeapRequest) GetQuery() string  { return r.Query }
func (r *HeapRequest) GetMaskEq() string { return r.MaskEq }
func (r *HeapRequest) GetNote() string   { return r.Note }

// HeapServer is the generated gRPC server interface. Handlers seed structurally
// (request params = source_params) — emit.go's requestParamIdxs needs the
// package path to contain "/pb". A case that is not a handler on this interface
// has empty source_seeds, report.rs:54 skips it, and the fixture proves
// nothing (HANDOFF trap 3).
type HeapServer interface {
	HandlePlainSend(context.Context, *HeapRequest) error
	HandleSelectSend(context.Context, *HeapRequest) error
	HandleCopyHop(context.Context, *HeapRequest) error
	HandleTwoFields(context.Context, *HeapRequest) error
	HandleUnread(context.Context, *HeapRequest) error
	HandleSBPArm(context.Context, *HeapRequest) error
	HandleDFAArm(context.Context, *HeapRequest) error
	HandleRawIface(context.Context, *HeapRequest) error
	HandleMixedSBP(context.Context, *HeapRequest) error
	HandleMixedDFA(context.Context, *HeapRequest) error
}

type UnimplementedHeapServer struct{}

func (UnimplementedHeapServer) HandlePlainSend(context.Context, *HeapRequest) error  { return nil }
func (UnimplementedHeapServer) HandleSelectSend(context.Context, *HeapRequest) error { return nil }
func (UnimplementedHeapServer) HandleCopyHop(context.Context, *HeapRequest) error    { return nil }
func (UnimplementedHeapServer) HandleTwoFields(context.Context, *HeapRequest) error  { return nil }
func (UnimplementedHeapServer) HandleUnread(context.Context, *HeapRequest) error     { return nil }
func (UnimplementedHeapServer) HandleSBPArm(context.Context, *HeapRequest) error     { return nil }
func (UnimplementedHeapServer) HandleDFAArm(context.Context, *HeapRequest) error     { return nil }
func (UnimplementedHeapServer) HandleRawIface(context.Context, *HeapRequest) error   { return nil }
func (UnimplementedHeapServer) HandleMixedSBP(context.Context, *HeapRequest) error   { return nil }
func (UnimplementedHeapServer) HandleMixedDFA(context.Context, *HeapRequest) error   { return nil }
