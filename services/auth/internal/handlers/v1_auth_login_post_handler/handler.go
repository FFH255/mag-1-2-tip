package v1_auth_login_post_handler

import (
	"context"
	"github.com/FFH255/mag-1-2-tip/services/auth/internal/models"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/FFH255/mag-1-2-tip/shared/httpx"
)

const handlerPath = "/v1/auth/login"

type loginService interface {
	Login(ctx context.Context, login, password string) (models.AccessToken, models.AccessTokenType, error)
}

type Handler struct {
	service loginService
}

func New(service loginService) httpx.Handler {
	return &Handler{
		service: service,
	}
}

func (h Handler) Handle(c *gin.Context) {
	ctx := c.Request.Context()

	requestBody, err := newRequestBody(c)
	if err != nil {
		httpx.WriteBadRequestError(c, err)
		return
	}

	accessToken, accessTokenType, err := h.service.Login(ctx, requestBody.Login, requestBody.Password)
	if err != nil {
		httpx.WriteInternalServerError(c, err)
		return
	}

	responseBody := newResponseBody(accessToken, accessTokenType)
	httpx.WriteOk(c, responseBody)
}

func (h Handler) Path() string {
	return handlerPath
}

func (h Handler) Method() string {
	return http.MethodPost
}
