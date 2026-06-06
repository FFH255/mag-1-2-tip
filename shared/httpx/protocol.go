package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type Error struct {
	Error string `json:"error" example:"something went wrong"`
}

func WriteError(c *gin.Context, status int, err any) {
	var response Error

	switch e := err.(type) {
	case error:
		response = Error{Error: e.Error()}
	case string:
		response = Error{Error: e}
	}

	c.AbortWithStatusJSON(status, response)
}

func WriteBadRequestError(c *gin.Context, err error) {
	WriteError(c, http.StatusBadRequest, err)
}

func WriteInternalServerError(c *gin.Context, err error) {
	WriteError(c, http.StatusInternalServerError, err)
}

func WriteNotFoundError(c *gin.Context, err error) {
	WriteError(c, http.StatusNotFound, err)
}

func WriteUnauthorizedError(c *gin.Context, err error) {
	WriteError(c, http.StatusUnauthorized, err)
}

func WriteJSON[V any](c *gin.Context, status int, v V) {
	c.JSON(status, v)
}

func WriteOk[V any](c *gin.Context, v V) {
	WriteJSON[V](c, http.StatusOK, v)
}
