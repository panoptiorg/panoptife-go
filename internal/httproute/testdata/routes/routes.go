// Package routes exercises the route resolver's edge cases on net/http alone:
// the default mux, middleware, factories, http.Handler values, a dynamic
// prefix, a router kept in a struct field, a mount through middleware, and
// the two things that must NOT become routes.
package routes

import (
	"net/http"
	"os"
)

func init() { http.HandleFunc("/healthz", health) }

func health(w http.ResponseWriter, r *http.Request)  {}
func health2(w http.ResponseWriter, r *http.Request) {}
func pets(w http.ResponseWriter, r *http.Request)    {}
func things(w http.ResponseWriter, r *http.Request)  {}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
}

func makeHandler(greeting string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(greeting)) }
}

type itemsAPI struct{}

func (itemsAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {}

type server struct{ mux *http.ServeMux }

func (s *server) routes() { s.mux.HandleFunc("GET /field", health2) }

func base() string { return os.Getenv("BASE") }

func Wire() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /logged", logging(http.HandlerFunc(health2)))
	mux.Handle("GET /made", makeHandler("hi"))
	mux.Handle("/items/", itemsAPI{})
	// oapi-codegen's std-http shape: a dynamic server prefix, kept as suffix
	mux.HandleFunc("GET "+base()+"/pets/{id}", pets)
	// a path that is nothing but a hole: no route
	mux.HandleFunc(base(), health2)

	api := http.NewServeMux()
	api.HandleFunc("POST /v2/things", things)
	mux.Handle("/api/", http.StripPrefix("/api", logging(api)))

	srv := &server{mux: http.NewServeMux()}
	srv.routes()
	mux.Handle("/srv/", srv.mux)
	return mux
}

// Register is exported and never called here: its mux has an unknown
// prefix, and its handler is a parameter, which does not resolve.
func Register(mux *http.ServeMux, h http.HandlerFunc) {
	mux.HandleFunc("GET /unrooted", h)
	mux.HandleFunc("GET /known", health)
}
