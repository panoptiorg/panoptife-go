package pb

import "context"

type Req struct {
	Q   string
	Raw []byte
}

// LibServer is the generated gRPC server interface. Handlers seed structurally
// (request params = source_params); a case that is not a handler on this
// interface has no source and proves nothing (HANDOFF trap 3).
type LibServer interface {
	Builder(context.Context, *Req) error
	Buffer(context.Context, *Req) error
	Unmarshal(context.Context, *Req) error
	Decode(context.Context, *Req) error
	Fprintf(context.Context, *Req) error
	Copy(context.Context, *Req) error
	Values(context.Context, *Req) error
	MapsCopy(context.Context, *Req) error
	Template(context.Context, *Req) error
	Base64(context.Context, *Req) error
	ThirdParty(context.Context, *Req) error
	CleanBuilder(context.Context, *Req) error
	CleanUnmarshal(context.Context, *Req) error
}

type UnimplementedLibServer struct{}

func (UnimplementedLibServer) Builder(context.Context, *Req) error { return nil }
