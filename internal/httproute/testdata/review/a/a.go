// Package a holds route shapes the review of coverage wave 1 found mishandled
// (items 1, 5, 7, 8, 11 and a StripPrefix mount, item 3).
package a

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
)

type S struct{}

func (s *S) routes(r chi.Router) { r.Get("/bound", s.h) }

func (s *S) h(w http.ResponseWriter, r *http.Request) {}

// item 1: a factory whose argument is a dependency, not the handler
func encodeJSON(w http.ResponseWriter, v any) {}

func makeHandler(enc func(http.ResponseWriter, any)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { enc(w, r.URL.Path) }
}

// a middleware whose argument IS the handler
func logged(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { next(w, r) }
}

func plain(w http.ResponseWriter, r *http.Request) {}

func ginh(c *gin.Context) {}

func Serve() {
	s := &S{}
	r := chi.NewRouter()
	r.Route("/p1", s.routes)
	r.Get("/p3", makeHandler(encodeJSON))
	r.Get("/logged", logged(plain))
	// item 5: a non-constant middle prefix
	ver := os.Getenv("VER")
	r.Route("/api", func(r chi.Router) {
		r.Route(ver, func(r chi.Router) {
			r.Get("/mid", plain)
		})
	})
	// item 7: a gin relative path with no leading slash
	g := gin.New()
	v1 := g.Group("/v1")
	v1.GET("users", ginh)
	// item 8: the gin Use chain keeps the group
	v1.Use(func(c *gin.Context) {}).GET("/used", ginh)

	// item 11: net/http host, exact and subtree patterns
	mux := http.NewServeMux()
	mux.HandleFunc("GET example.com/x/{$}", plain)
	mux.HandleFunc("example.com/", plain)
	mux.HandleFunc("/static/", plain)
	// item 3: StripPrefix strips "/api" from "/api/v2/…"
	sub := http.NewServeMux()
	sub.HandleFunc("/y", plain)
	mux.Handle("/api/v2/", http.StripPrefix("/api", sub))
	_ = http.ListenAndServe(":1", mux)
	_ = http.ListenAndServe(":2", r)
	_ = http.ListenAndServe(":3", g)
}
