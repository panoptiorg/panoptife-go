// Command httpapi serves one API through five routers mounted on one
// net/http ServeMux. It is a coverage wave 1 fixture: every route's method and
// path is asserted exactly by pc-fe's tests.
package main

import (
	"database/sql"
	"net/http"
	"os"

	"example.com/httpapi/chiapi"
	"example.com/httpapi/echoapi"
	"example.com/httpapi/ginapi"
	"example.com/httpapi/gorillaapi"
	"example.com/httpapi/stdapi"
	"example.com/httpapi/store"
)

func main() {
	db, err := sql.Open("postgres", os.Getenv("DSN"))
	if err != nil {
		panic(err)
	}
	st := store.New(db)
	root := http.NewServeMux()
	stdapi.Register(root, st)
	root.Handle("/chi/", http.StripPrefix("/chi", chiapi.New(st)))
	root.Handle("/gin/", ginapi.New(st))
	root.Handle("/gorilla/", gorillaapi.New(st))
	root.Handle("/echo/", echoapi.New(st))
	_ = http.ListenAndServe(":8080", root)
}
