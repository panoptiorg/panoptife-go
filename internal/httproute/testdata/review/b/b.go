// Package b holds the review's StripPrefix-under-a-group (item 3) and
// fact-cap (item 9) shapes.
package b

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/mux"
)

func plain(w http.ResponseWriter, r *http.Request)  {}
func plain2(w http.ResponseWriter, r *http.Request) {}

func sub() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("/x", plain)
	return m
}

func reg(r chi.Router) { r.Get("/shared", plain2) }

func Serve() {
	// item 3: StripPrefix under a gorilla PathPrefix and a chi Route strips
	// from the URL, not from the group prefix
	g := mux.NewRouter()
	g.PathPrefix("/api").Handler(http.StripPrefix("/api", sub()))
	c := chi.NewRouter()
	c.Route("/v1", func(r chi.Router) {
		r.Handle("/files/*", http.StripPrefix("/v1/files", sub()))
	})

	// item 9: nine prefixes reach one register function; the analysis keeps 8
	reg(c.Route("/p1", nil))
	reg(c.Route("/p2", nil))
	reg(c.Route("/p3", nil))
	reg(c.Route("/p4", nil))
	reg(c.Route("/p5", nil))
	reg(c.Route("/p6", nil))
	reg(c.Route("/p7", nil))
	reg(c.Route("/p8", nil))
	reg(c.Route("/p9", nil))
	_ = http.ListenAndServe(":1", g)
	_ = http.ListenAndServe(":2", c)
}
