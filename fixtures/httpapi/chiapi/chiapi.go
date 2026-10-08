// Package chiapi is a chi v5 router: Route, Group, With, Mount, a router
// handed to a register function, and handlers as method values.
package chiapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"example.com/httpapi/store"
)

type Handler struct{ st *store.Store }

type order struct {
	Note string `json:"note"`
}

// New returns the router as an http.Handler; main mounts it under /chi with
// http.StripPrefix, so every path below gains that prefix.
func New(st *store.Store) http.Handler {
	h := &Handler{st: st}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Route("/v1", func(r chi.Router) {
		registerOrders(r, h)
		r.Group(func(r chi.Router) {
			r.Use(requireToken)
			r.Delete("/orders/{orderID}", h.DeleteOrder)
		})
	})
	r.Mount("/admin", adminRouter(h))
	r.With(middleware.NoCache).Get("/health", health)
	return r
}

func registerOrders(r chi.Router, h *Handler) {
	r.Post("/orders", h.CreateOrder)
	r.Get("/orders/{orderID:[0-9]+}", h.GetOrder)
}

func adminRouter(h *Handler) chi.Router {
	r := chi.NewRouter()
	r.Get("/audit", h.Audit)
	r.Method(http.MethodPut, "/flags/{name}", http.HandlerFunc(h.SetFlag))
	return r
}

func requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func health(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }

func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
	var o order
	if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = h.st.Exec(r.Context(), "INSERT INTO orders(note) VALUES ('"+o.Note+"')")
}

func (h *Handler) GetOrder(w http.ResponseWriter, r *http.Request) {
	_ = h.st.Exec(r.Context(), "SELECT * FROM orders WHERE id = "+chi.URLParam(r, "orderID"))
}

func (h *Handler) DeleteOrder(w http.ResponseWriter, r *http.Request) {
	_ = h.st.Exec(r.Context(), "DELETE FROM orders WHERE id = "+chi.URLParam(r, "orderID"))
}

func (h *Handler) Audit(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte("audit"))
}

func (h *Handler) SetFlag(w http.ResponseWriter, r *http.Request) {
	_ = h.st.Exec(r.Context(), "UPDATE flags SET on = true WHERE name = '"+chi.URLParam(r, "name")+"'")
}
