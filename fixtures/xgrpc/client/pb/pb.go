// Package pb is generated-style protobuf output (client side): message
// structs that embed the protoimpl state field, trivial getters, and the
// generated gRPC client interface.
package pb

import (
	"context"

	"google.golang.org/protobuf/runtime/protoimpl"
)

// StoreGetRequest has two disjoint fields: Id carries untrusted request data,
// Note is written from a constant.
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

// StoreClient is the generated gRPC client interface.
type StoreClient interface {
	Get(ctx context.Context, in *StoreGetRequest) (*Resp, error)
}
