package main

import (
	"log"
	"net/http"
	"os"

	handlers "prac_01/services/auth/internal/http"
	"prac_01/services/auth/internal/service"
	"prac_01/shared/middleware"
)

func main() {
	port := os.Getenv("AUTH_PORT")
	if port == "" {
		port = "8081"
	}

	mux := http.NewServeMux()
	handler := handlers.NewHandler(service.NewAuthService())
	handler.Register(mux)

	wrapped := middleware.RequestID(middleware.Logging("auth")(mux))

	addr := ":" + port
	log.Printf("auth service started on %s", addr)
	if err := http.ListenAndServe(addr, wrapped); err != nil {
		log.Fatal(err)
	}
}
