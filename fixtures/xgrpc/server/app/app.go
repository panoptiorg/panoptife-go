// Package app is the server: the handler reads the request fields through
// the (bodiless, pb out of scope) generated getters into two different
// sinks. Id is the field that carries the client's untrusted data; Note is
// clean and must stay so. A helper that merely receives the request has no
// endpoint parameter — its only source is the catalog's getter rule.
package app

import (
	"context"
	"database/sql"
	"os/exec"

	"example.com/xgrpc/server/pb"
)

var db *sql.DB

type Implementation struct {
	pb.UnimplementedStoreServer
}

// Get dispatches: Id goes to the SQL sink, Note to the command sink.
func (i *Implementation) Get(ctx context.Context, req *pb.StoreGetRequest) (*pb.Resp, error) {
	if _, err := db.Exec(req.GetId()); err != nil {
		return nil, err
	}
	_ = exec.Command(req.GetNote())
	return &pb.Resp{Ok: true}, nil
}

// Render is the helper shape the getter rule exists for: no endpoint
// parameter, so nothing structural seeds it.
func Render(req *pb.StoreGetRequest) string {
	if _, err := db.Exec(req.GetId()); err != nil {
		return ""
	}
	return req.GetNote()
}
