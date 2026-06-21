package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"

	"google.golang.org/grpc"
	authv1 "prac_09/gen/auth/v1"
	authgrpc "prac_09/services/auth/internal/grpc"
	authhttp "prac_09/services/auth/internal/http"
	"prac_09/services/auth/internal/service"
	"prac_09/shared/logger"
	"prac_09/shared/middleware"
)

func main() {
	log := logger.New("auth")

	httpPort := os.Getenv("AUTH_PORT")
	if httpPort == "" {
		httpPort = "8085"
	}

	grpcPort := os.Getenv("AUTH_GRPC_PORT")
	if grpcPort == "" {
		grpcPort = "50051"
	}

	authService := service.NewAuthService()

	mux := http.NewServeMux()
	handler := authhttp.NewHandler(authService, log)
	handler.Register(mux)

	httpServer := &http.Server{
		Addr:    ":" + httpPort,
		Handler: middleware.RequestID(middleware.SecurityHeaders(middleware.AccessLog(log)(mux))),
	}

	grpcListener, err := net.Listen("tcp", ":"+grpcPort)
	if err != nil {
		log.WithError(err).Fatal("failed to listen grpc port")
	}

	grpcServer := grpc.NewServer()
	authv1.RegisterAuthServiceServer(grpcServer, authgrpc.NewServer(authService, log))

	errCh := make(chan error, 2)

	go func() {
		log.WithFields(logrus.Fields{"port": httpPort, "transport": "http"}).Info("server started")
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	go func() {
		log.WithFields(logrus.Fields{"port": grpcPort, "transport": "grpc"}).Info("server started")
		if err := grpcServer.Serve(grpcListener); err != nil {
			errCh <- err
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errCh:
		log.WithError(err).Fatal("server error")
	case <-ctx.Done():
		log.Info("shutdown requested")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-shutdownCtx.Done():
		grpcServer.Stop()
	}

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.WithError(err).Error("http shutdown error")
	}
}
