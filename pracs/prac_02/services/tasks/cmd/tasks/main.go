package main

import (
	"log"
	"net/http"
	"os"

	tasksauth "prac_02/services/tasks/internal/client/authclient"
	taskshttp "prac_02/services/tasks/internal/http"
	"prac_02/services/tasks/internal/service"
	"prac_02/shared/middleware"
)

func main() {
	port := os.Getenv("TASKS_PORT")
	if port == "" {
		port = "8084"
	}

	authGRPCAddr := os.Getenv("AUTH_GRPC_ADDR")
	if authGRPCAddr == "" {
		authGRPCAddr = "localhost:50051"
	}

	authClient, err := tasksauth.New(authGRPCAddr)
	if err != nil {
		log.Fatal(err)
	}
	defer authClient.Close()

	mux := http.NewServeMux()
	handler := taskshttp.NewHandler(service.NewTaskService(), authClient)
	handler.Register(mux)

	wrapped := middleware.RequestID(middleware.Logging("tasks")(mux))

	addr := ":" + port
	log.Printf("tasks service started on %s, auth_grpc=%s", addr, authGRPCAddr)
	if err := http.ListenAndServe(addr, wrapped); err != nil {
		log.Fatal(err)
	}
}
