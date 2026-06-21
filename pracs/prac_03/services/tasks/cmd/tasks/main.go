package main

import (
	"net/http"
	"os"

	tasksauth "prac_03/services/tasks/internal/client/authclient"
	taskshttp "prac_03/services/tasks/internal/http"
	"prac_03/services/tasks/internal/service"
	"prac_03/shared/logger"
	"prac_03/shared/middleware"
)

func main() {
	log := logger.New("tasks")

	port := os.Getenv("TASKS_PORT")
	if port == "" {
		port = "8086"
	}

	authGRPCAddr := os.Getenv("AUTH_GRPC_ADDR")
	if authGRPCAddr == "" {
		authGRPCAddr = "localhost:50051"
	}

	authClient, err := tasksauth.New(authGRPCAddr, log)
	if err != nil {
		log.WithError(err).Fatal("failed to connect to auth service")
	}
	defer authClient.Close()

	mux := http.NewServeMux()
	handler := taskshttp.NewHandler(service.NewTaskService(), authClient, log)
	handler.Register(mux)

	wrapped := middleware.RequestID(middleware.AccessLog(log)(mux))

	addr := ":" + port
	log.WithField("port", port).WithField("auth_grpc", authGRPCAddr).Info("server started")
	if err := http.ListenAndServe(addr, wrapped); err != nil {
		log.WithError(err).Fatal("server error")
	}
}
