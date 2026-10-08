// Package requests holds inbound and outgoing *http.Request values side by
// side: a read: site belongs on the first kind only (review item 12).
package requests

import (
	"context"
	"net/http"
)

func use(...any) {}

// --- inbound: these keep their read: sites ---

func Param(w http.ResponseWriter, r *http.Request) { use(r.Body) }

type reqCtx struct{ Req *http.Request }

func (c *reqCtx) Request() *http.Request { return c.Req }

// a framework context holding the request (gin's c.Request)
func FieldOfParam(c *reqCtx) { use(c.Req.Header) }

// an accessor's result (echo's c.Request())
func Accessor(c *reqCtx) { use(c.Request().URL) }

// a middleware's r.WithContext(ctx) is still the inbound request
func Derived(w http.ResponseWriter, r *http.Request) {
	r2 := r.WithContext(context.Background())
	use(r2.Form)
}

// --- outgoing: no read: sites ---

func Built(ctx context.Context) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://svc/x", nil)
	req.Header.Set("Accept", "application/json")
	_ = req.URL
	old, _ := http.NewRequest(http.MethodGet, "http://svc/y", nil)
	use(old.Host)
}

func Literal() {
	req := &http.Request{Method: http.MethodGet}
	use(req.Header)
}

func Cloned(ctx context.Context) {
	req, _ := http.NewRequest(http.MethodGet, "http://svc/x", nil)
	use(req.Clone(ctx).Header)
}

func FromResponse(resp *http.Response) { use(resp.Request.URL) }
