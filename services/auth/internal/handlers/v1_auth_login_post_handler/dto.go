package v1_auth_login_post_handler

import (
	"github.com/FFH255/mag-1-2-tip/services/auth/internal/models"
	"github.com/gin-gonic/gin"
)

type RequestBody struct {
	Login    string `json:"login" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func newRequestBody(c *gin.Context) (*RequestBody, error) {
	var body RequestBody
	if err := c.ShouldBindJSON(&body); err != nil {
		return nil, err
	}

	return &body, nil
}

type ResponseBody struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
}

func newResponseBody(accessToken models.AccessToken, accessTokenType models.AccessTokenType) *ResponseBody {
	return &ResponseBody{
		AccessToken: accessToken.String(),
		TokenType:   accessTokenType.String(),
	}
}
