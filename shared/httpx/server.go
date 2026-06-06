package httpx

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type Server struct {
	handler                 http.Handler
	address                 string
	gracefulShutdownTimeout time.Duration
	readTimeout             time.Duration
	writeTimeout            time.Duration
	idleTimeout             time.Duration
}

func (s *Server) MustRun(ctx context.Context) {
	server := &http.Server{
		Handler:      s.handler,
		Addr:         s.address,
		WriteTimeout: s.writeTimeout,
		ReadTimeout:  s.readTimeout,
		IdleTimeout:  s.idleTimeout,
	}

	go func() {
		done := make(chan os.Signal, 1)
		signal.Notify(done, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
		<-done

		ctx, cancel := context.WithTimeout(
			ctx,
			s.gracefulShutdownTimeout,
		)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			log.Fatalf("Server Shutdown Failed:%+v", err)
		}
	}()

	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Server Shutdown Failed:%+v", err)
	}
}

func NewServer(handler http.Handler, address string, gracefulShutdownTimeout, readTimeout, writeTimeout, idleTimeout time.Duration) *Server {
	return &Server{
		handler:                 handler,
		address:                 address,
		gracefulShutdownTimeout: gracefulShutdownTimeout,
		readTimeout:             readTimeout,
		writeTimeout:            writeTimeout,
		idleTimeout:             idleTimeout,
	}
}
