package httpclient

import "net/http"

// Mux serves Handle, so the client repo has an HTTP entry point of its own.
func Mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /signup", Handle)
	return mux
}
