package pb

import "context"

type ContainerRequest struct {
	Query string
	IDs   []string
}

func (r *ContainerRequest) GetQuery() string { return r.Query }
func (r *ContainerRequest) GetIDs() []string { return r.IDs }

// ContainerServer is the generated gRPC server interface. Handlers seed
// structurally (request params = source_params); a case that is not a handler
// on this interface has no source and proves nothing (HANDOFF trap 3).
type ContainerServer interface {
	MapLocal(context.Context, *ContainerRequest) error
	MapLiteral(context.Context, *ContainerRequest) error
	MapKeysRange(context.Context, *ContainerRequest) error
	MapInField(context.Context, *ContainerRequest) error
	ChanLocal(context.Context, *ContainerRequest) error
	ChanWorker(context.Context, *ContainerRequest) error
	SelectSend(context.Context, *ContainerRequest) error
	ChanProducer(context.Context, *ContainerRequest) error
	MapOther(context.Context, *ContainerRequest) error
	ChanOther(context.Context, *ContainerRequest) error
}

type UnimplementedContainerServer struct{}

func (UnimplementedContainerServer) MapLocal(context.Context, *ContainerRequest) error { return nil }
