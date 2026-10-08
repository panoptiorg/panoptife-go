// Package httpclient calls fixtures/httpapi over HTTP. It is a coverage wave
// 1 fixture: each client call's method and URL template is asserted, and the
// core links the template to the server's route by suffix (the base URL is
// configuration, so only the path after it is known).
package httpclient

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

var apiBase = os.Getenv("API_URL")

// encoder is dispatched through an interface with one implementation, so a
// cgstore snapshot of this module persists call targets in functions that
// also carry synthetic `read:` and `http:` sites.
type encoder interface{ encode(name string) string }

type jsonEncoder struct{}

func (jsonEncoder) encode(name string) string { return `{"name":"` + name + `"}` }

var enc encoder = jsonEncoder{}

// Handle takes untrusted input and forwards it to the user API.
func Handle(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	id := r.URL.Query().Get("id")
	if err := CreateUser(r.Context(), apiBase, name); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	resp, err := GetUser(apiBase, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
}

// CreateUser: POST {base}/api/users with the name in the body.
func CreateUser(ctx context.Context, baseURL, name string) error {
	body := strings.NewReader(enc.encode(name))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/users", body)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// GetUser: GET {base}/api/users/{id}.
func GetUser(base, id string) (*http.Response, error) {
	return http.Get(fmt.Sprintf("%s/api/users/%s", base, id))
}

// Stock posts to a fully constant URL: the scheme and host are dropped.
func Stock(c *http.Client, sku string) error {
	resp, err := c.PostForm("http://inventory:8080/v1/stock/reserve", url.Values{"sku": {sku}})
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// Search builds its URL with url.JoinPath and takes the method from a
// parameter: the path is known, the method is not.
func Search(ctx context.Context, method, q string) error {
	u, err := url.JoinPath(apiBase, "api", "search", q)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(method, u, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
