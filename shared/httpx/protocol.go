package httpx

import (
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

func WriteJSON[V any](c *gin.Context, status int, v V) {
	c.JSON(status, v)
}
