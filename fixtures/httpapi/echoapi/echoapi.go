// Package echoapi is an echo v4 server: a group and a route with middleware
// after the handler.
package echoapi

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"example.com/httpapi/store"
)

func New(st *store.Store) http.Handler {
	e := echo.New()
	g := e.Group("/echo")
	g.GET("/greet/:name", greet)
	e.POST("/echo/search", func(c echo.Context) error {
		return st.Exec(c.Request().Context(), "SELECT * FROM docs WHERE body LIKE '%"+c.FormValue("q")+"%'")
	}, logRequests)
	return e
}

func greet(c echo.Context) error {
	return c.String(http.StatusOK, "hello "+c.Param("name"))
}

func logRequests(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error { return next(c) }
}
