package httpx

import "github.com/gin-gonic/gin"

type Handler interface {
	Handle(c *gin.Context)
	Path() string
	Method() string
}

func RegisterHandler(engine *gin.Engine, handler Handler, middlewares ...gin.HandlerFunc) {
	engine.Handle(handler.Method(), handler.Path(), append(middlewares, handler.Handle)...)
}
