package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	authv1 "prac_02/gen/auth/v1"
	authgrpc "prac_02/services/auth/internal/grpc"
	authhttp "prac_02/services/auth/internal/http"
	"prac_02/services/auth/internal/service"
	"prac_02/shared/middleware"
)

func main() {
	httpPort := os.Getenv("AUTH_PORT")
	if httpPort == "" {
		httpPort = "8083"
	}

	grpcPort := os.Getenv("AUTH_GRPC_PORT")
	if grpcPort == "" {
		grpcPort = "50051"
	}

	authService := service.NewAuthService()

	mux := http.NewServeMux()
	handler := authhttp.NewHandler(authService)
	handler.Register(mux)

	httpServer := &http.Server{
		Addr:    ":" + httpPort,
		Handler: middleware.RequestID(middleware.Logging("auth")(mux)),
	}

	grpcListener, err := net.Listen("tcp", ":"+grpcPort)
	if err != nil {
		log.Fatal(err)
	}

	grpcServer := grpc.NewServer()
	authv1.RegisterAuthServiceServer(grpcServer, authgrpc.NewServer(authService))

	errCh := make(chan error, 2)

	go func() {
		log.Printf("auth http service started on :%s", httpPort)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	go func() {
		log.Printf("auth grpc service started on :%s", grpcPort)
		if err := grpcServer.Serve(grpcListener); err != nil {
			errCh <- err
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errCh:
		log.Fatal(err)
	case <-ctx.Done():
		log.Print("auth shutdown requested")
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
		log.Printf("auth http shutdown error: %v", err)
	}
}
