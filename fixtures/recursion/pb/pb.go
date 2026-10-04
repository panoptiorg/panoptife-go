// Package pb is a stand-in for protoc-gen-go-grpc output: a RecursionSvcServer
// interface, its Unimplemented<Svc>Server embed (so app.Implementation is
// structurally seeded as a gRPC server), and the message types each handler
// needs to exercise one recursion-engine case (doc: fixtures/recursion).
package pb

import "context"

// Node is a singly-linked list dressed as a tree node — enough shape for the
// self-recursive walk/last/direct cases (Child == nil is the base case).
type Node struct {
	Name  string
	Child *Node
}

func (n *Node) GetChild() *Node { return n.Child }
func (n *Node) GetName() string { return n.Name }

type WalkTreeRequest struct{ Root *Node }

func (r *WalkTreeRequest) GetRoot() *Node { return r.Root }

type WalkTreeResponse struct{ Ok bool }

type RecurseReturnRequest struct{ Root *Node }

func (r *RecurseReturnRequest) GetRoot() *Node { return r.Root }

type RecurseReturnResponse struct{ Last string }

type DirectSinkRequest struct{ Root *Node }

func (r *DirectSinkRequest) GetRoot() *Node { return r.Root }

type DirectSinkResponse struct{ Ok bool }

type MutualRecursionRequest struct {
	Name string
	N    int32
}

func (r *MutualRecursionRequest) GetName() string { return r.Name }
func (r *MutualRecursionRequest) GetN() int32     { return r.N }

type MutualRecursionResponse struct{ Ok bool }

type DiamondRequest struct{ Name string }

func (r *DiamondRequest) GetName() string { return r.Name }

type DiamondResponse struct{ Ok bool }

type PickSecondRequest struct{ Name string }

func (r *PickSecondRequest) GetName() string { return r.Name }

type PickSecondResponse struct{ Ok bool }

// RecursionSvcServer is the generated gRPC server interface.
type RecursionSvcServer interface {
	WalkTree(context.Context, *WalkTreeRequest) (*WalkTreeResponse, error)
	RecurseReturn(context.Context, *RecurseReturnRequest) (*RecurseReturnResponse, error)
	DirectSink(context.Context, *DirectSinkRequest) (*DirectSinkResponse, error)
	MutualRecursion(context.Context, *MutualRecursionRequest) (*MutualRecursionResponse, error)
	Diamond(context.Context, *DiamondRequest) (*DiamondResponse, error)
	PickSecond(context.Context, *PickSecondRequest) (*PickSecondResponse, error)
}

// UnimplementedRecursionSvcServer is embedded by server implementations.
type UnimplementedRecursionSvcServer struct{}

func (UnimplementedRecursionSvcServer) WalkTree(context.Context, *WalkTreeRequest) (*WalkTreeResponse, error) {
	return nil, nil
}

func (UnimplementedRecursionSvcServer) RecurseReturn(context.Context, *RecurseReturnRequest) (*RecurseReturnResponse, error) {
	return nil, nil
}

func (UnimplementedRecursionSvcServer) DirectSink(context.Context, *DirectSinkRequest) (*DirectSinkResponse, error) {
	return nil, nil
}

func (UnimplementedRecursionSvcServer) MutualRecursion(context.Context, *MutualRecursionRequest) (*MutualRecursionResponse, error) {
	return nil, nil
}

func (UnimplementedRecursionSvcServer) Diamond(context.Context, *DiamondRequest) (*DiamondResponse, error) {
	return nil, nil
}

func (UnimplementedRecursionSvcServer) PickSecond(context.Context, *PickSecondRequest) (*PickSecondResponse, error) {
	return nil, nil
}
