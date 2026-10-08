// Package stdapi serves the user API on a Go 1.22 net/http ServeMux: method
// patterns, a path wildcard, a catch-all, and a mux handed in by the caller.
package stdapi

import (
	"encoding/json"
	"net/http"

	"example.com/httpapi/store"
)

type Server struct{ st *store.Store }

type createUserReq struct {
	Name string `json:"name"`
}

// Register is called from main with the root mux: the routes' prefix comes
// from the caller, through the parameter.
func Register(mux *http.ServeMux, st *store.Store) {
	s := &Server{st: st}
	mux.HandleFunc("POST /api/users", s.createUser)
	mux.HandleFunc("GET /api/users/{id}", s.getUser)
	mux.HandleFunc("GET /api/stats", s.stats)
	mux.Handle("GET /files/{path...}", http.HandlerFunc(s.file))
}

// createUser: the request body reaches SQL through json.Decoder (E2).
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req createUserReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.st.CreateUser(r.Context(), req.Name); err != nil {
		http.Error(w, "store", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	name, err := s.st.FindUser(r.Context(), r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"name": name})
}

// stats passes only the request context to a constant query (E3 negative).
func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	n, err := s.st.CountUsers(r.Context())
	if err != nil {
		http.Error(w, "store", http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]int{"users": n})
}

func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "/srv/files/"+r.PathValue("path"))
}
