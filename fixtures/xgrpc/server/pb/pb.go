// Package pb is generated-style protobuf output (server side): the same
// contract's message structs and server interface, generated into this
// module separately — exactly how two services share one proto.
package pb

import (
	"context"

	"google.golang.org/protobuf/runtime/protoimpl"
)

// StoreGetRequest is the server's generated type for the same proto message.
type StoreGetRequest struct {
	state protoimpl.MessageState
	Id    string
	Note  string
}

func (x *StoreGetRequest) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *StoreGetRequest) GetNote() string {
	if x != nil {
		return x.Note
	}
	return ""
}

type Resp struct {
	state protoimpl.MessageState
	Ok    bool
}

func (x *Resp) GetOk() bool {
	if x != nil {
		return x.Ok
	}
	return false
}

// StoreServer is the generated gRPC server interface.
type StoreServer interface {
	Get(context.Context, *StoreGetRequest) (*Resp, error)
}

// UnimplementedStoreServer is embedded by server implementations.
type UnimplementedStoreServer struct{}

func (UnimplementedStoreServer) Get(context.Context, *StoreGetRequest) (*Resp, error) {
	return nil, nil
}
