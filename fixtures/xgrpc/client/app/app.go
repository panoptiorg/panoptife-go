// Package app is the client service: a net/http handler turns untrusted
// input into one request field and calls the remote contract. The other
// field is written from a constant.
package app

import (
	"net/http"

	"example.com/xgrpc/client/pb"
)

type Store struct {
	client pb.StoreClient
}

// Handle seeds the Id field from a query parameter; Note stays clean.
func (s *Store) Handle(w http.ResponseWriter, r *http.Request) {
	_, _ = s.client.Get(r.Context(), &pb.StoreGetRequest{
		Id:   r.FormValue("id"),
		Note: "clean",
	})
}
