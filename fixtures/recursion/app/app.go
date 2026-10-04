// Package app implements RecursionSvc: one handler per engine recursion case
// (see the fixture's top-level comment in ../pb/pb.go). Each handler is
// named exactly for the case it exercises so the core's e2e assert can key
// on the handler name.
package app

import (
	"context"

	"example.com/recursion/pb"
	"example.com/recursion/store"
)

type Implementation struct {
	pb.UnimplementedRecursionSvcServer
	storage *store.Storage
}

func New() *Implementation { return &Implementation{storage: &store.Storage{}} }

// --- WalkTree: self-recursion where the sink is reached ONLY through the
// recursive call, in a DIFFERENT parameter slot ---
//
// Taint propagation is control-flow insensitive, so a branch on depth would
// not hide anything: every sink in a body is seen in one pass. What a single
// pass cannot see is a fact that changes SLOT across the recursive call. Here
// the request node arrives as `n`, and the only path to the sink is
// `n.Name -> acc` in the recursive frame -> Execx(acc). A summary computed
// without iterating the recursion has no row for `n`, only for `acc`, and
// misses the chain entirely.
func walk(s *store.Storage, n *pb.Node, acc string) {
	if n == nil {
		s.Execx(acc) // SINK — reached by n.Name only as `acc` of the next frame
		return
	}
	walk(s, n.Child, n.Name)
}

func (i *Implementation) WalkTree(ctx context.Context, req *pb.WalkTreeRequest) (*pb.WalkTreeResponse, error) {
	walk(i.storage, req.GetRoot(), "")
	return &pb.WalkTreeResponse{Ok: true}, nil
}

// --- RecurseReturn: the param reaches the RETURN only via the recursive call ---

// last is self-recursive with an accumulator: `n` reaches the RETURN only by
// becoming `acc` in the next frame (same slot-swap shape as walk, on the
// return side). One pass yields Param(acc)->Return only; the fixpoint adds
// Param(n)->Return.
func last(n *pb.Node, acc string) string {
	if n == nil {
		return acc
	}
	return last(n.Child, n.Name)
}

func (i *Implementation) RecurseReturn(ctx context.Context, req *pb.RecurseReturnRequest) (*pb.RecurseReturnResponse, error) {
	row, err := i.storage.Selectx(last(req.GetRoot(), "")) // SINK: last(...) return -> Selectx
	if err != nil {
		return nil, err
	}
	return &pb.RecurseReturnResponse{Last: row}, nil
}

// --- DirectSink: control — self-recursion that hits the sink on every frame ---
//
// direct hits store.Execx on the param directly in every frame: exactly one
// chain, no duplication across recursion depth.
func direct(s *store.Storage, n *pb.Node) {
	s.Execx(n.Name) // SINK — every frame
	if n.Child != nil {
		direct(s, n.Child)
	}
}

func (i *Implementation) DirectSink(ctx context.Context, req *pb.DirectSinkRequest) (*pb.DirectSinkResponse, error) {
	direct(i.storage, req.GetRoot())
	return &pb.DirectSinkResponse{Ok: true}, nil
}

// --- MutualRecursion: control — a two-function SCC, sink in one of them ---

func ping(s *store.Storage, name string, n int32) {
	if n <= 0 {
		return
	}
	pong(s, name, n-1)
}

func pong(s *store.Storage, name string, n int32) {
	s.Execx(name) // SINK
	if n <= 0 {
		return
	}
	ping(s, name, n-1)
}

func (i *Implementation) MutualRecursion(ctx context.Context, req *pb.MutualRecursionRequest) (*pb.MutualRecursionResponse, error) {
	ping(i.storage, req.GetName(), req.GetN())
	return &pb.MutualRecursionResponse{Ok: true}, nil
}

// --- Diamond: two paths converge on one shared callee — must not read as a cycle ---

func a(s *store.Storage, x string) { shared(s, x) }
func b(s *store.Storage, x string) { shared(s, x) }

func shared(s *store.Storage, x string) {
	s.Execx(x) // SINK
}

func (i *Implementation) Diamond(ctx context.Context, req *pb.DiamondRequest) (*pb.DiamondResponse, error) {
	name := req.GetName()
	a(i.storage, name)
	b(i.storage, name)
	return &pb.DiamondResponse{Ok: true}, nil
}

// --- PickSecond: interface dispatch where VTA must yield BOTH impls ---

// Runner is satisfied by two impls; NoopRunner sorts before SinkRunner so a
// name-ordered dispatch listing puts the harmless one first.
type Runner interface {
	Run(s *store.Storage, name string)
}

type NoopRunner struct{}

func (*NoopRunner) Run(s *store.Storage, name string) {}

type SinkRunner struct{}

func (*SinkRunner) Run(s *store.Storage, name string) {
	s.Execx(name) // SINK
}

// runners is a package-level slice literal holding BOTH concrete types, so
// VTA's flow-based target set for Runner includes both NoopRunner and
// SinkRunner regardless of which index PickSecond happens to read.
var runners = []Runner{&NoopRunner{}, &SinkRunner{}}

func (i *Implementation) PickSecond(ctx context.Context, req *pb.PickSecondRequest) (*pb.PickSecondResponse, error) {
	r := runners[1] // the second runner — SinkRunner — but VTA sees both targets
	r.Run(i.storage, req.GetName())
	return &pb.PickSecondResponse{Ok: true}, nil
}
