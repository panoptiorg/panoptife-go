// Package gorillaapi is a gorilla/mux router with a PathPrefix subrouter and
// methods attached to the returned *Route.
package gorillaapi

import (
	"net/http"

	"github.com/gorilla/mux"

	"example.com/httpapi/store"
)

type notes struct{ st *store.Store }

func New(st *store.Store) http.Handler {
	h := &notes{st: st}
	r := mux.NewRouter()
	s := r.PathPrefix("/gorilla").Subrouter()
	s.HandleFunc("/notes/{id}", h.get).Methods(http.MethodGet)
	s.HandleFunc("/notes", h.create).Methods("POST", "PUT")
	// methods before the path: the *Route chain is read backwards too
	s.Methods(http.MethodDelete).Path("/notes/{id}").HandlerFunc(h.remove)
	return r
}

func (h *notes) get(w http.ResponseWriter, r *http.Request) {
	_ = h.st.Exec(r.Context(), "SELECT * FROM notes WHERE id = "+mux.Vars(r)["id"])
}

func (h *notes) remove(w http.ResponseWriter, r *http.Request) {
	_ = h.st.Exec(r.Context(), "DELETE FROM notes WHERE id = "+mux.Vars(r)["id"])
}

func (h *notes) create(w http.ResponseWriter, r *http.Request) {
	_ = h.st.Exec(r.Context(), "INSERT INTO notes(body) VALUES ('"+r.FormValue("body")+"')")
}
