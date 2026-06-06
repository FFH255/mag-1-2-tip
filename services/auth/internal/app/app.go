package app

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/FFH255/mag-1-2-tip/services/auth/internal/handlers/v1_auth_login_post_handler"
	"github.com/FFH255/mag-1-2-tip/services/auth/internal/repositories/users_repository"
	"github.com/FFH255/mag-1-2-tip/services/auth/internal/services/auth_service"
	"github.com/FFH255/mag-1-2-tip/shared/httpx"
)

type App struct {
	server *httpx.Server
}

func New() *App {
	config, err := parseConfig("services/auth/.env")
	if err != nil {
		panic(err)
	}

	usersRepository := users_repository.New()

	authService := auth_service.New(config.JWTSecret, usersRepository)

	v1AuthLoginPostHandler := v1_auth_login_post_handler.New(authService)

	router := gin.Default()
	httpx.RegisterHandler(router, v1AuthLoginPostHandler)

	server := httpx.NewServer(
		router.Handler(),
		config.HTTPAddress,
		config.GracefulShutdownTimeout,
		config.ReadTimeout,
		config.WriteTimeout,
		config.IdleTimeout,
	)

	return &App{
		server: server,
	}
}

func (a *App) Run(ctx context.Context) {
	a.server.MustRun(ctx)
}
