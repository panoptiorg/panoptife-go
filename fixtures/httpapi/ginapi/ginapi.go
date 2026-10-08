// Package ginapi is a gin engine: groups, a group handed to a register
// function, middleware ahead of the handler, and ShouldBindJSON into SQL (E1).
package ginapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"example.com/httpapi/store"
)

type itemHandler struct{ st *store.Store }

type itemReq struct {
	Name string `json:"name"`
}

// New returns the engine; main registers it on the root mux without
// StripPrefix, so the /gin prefix is carried by the group itself.
func New(st *store.Store) http.Handler {
	e := gin.New()
	api := e.Group("/gin")
	registerItems(api.Group("/items"), itemHandler{st: st})
	e.GET("/gin/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })
	return e
}

func registerItems(g *gin.RouterGroup, h itemHandler) {
	g.POST("", authRequired, h.create)
	g.GET("/:id", h.get)
}

func authRequired(c *gin.Context) {
	if c.GetHeader("Authorization") == "" {
		c.AbortWithStatus(http.StatusUnauthorized)
	}
}

func (h itemHandler) create(c *gin.Context) {
	var req itemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	_ = h.st.CreateItem(c, req.Name)
	c.Status(http.StatusCreated)
}

func (h itemHandler) get(c *gin.Context) {
	_ = h.st.Exec(c, "SELECT * FROM items WHERE id = '"+c.Param("id")+"'")
}
