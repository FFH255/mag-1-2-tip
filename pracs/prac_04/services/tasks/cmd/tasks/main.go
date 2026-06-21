package main

import (
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	tasksauth "prac_04/services/tasks/internal/client/authclient"
	taskshttp "prac_04/services/tasks/internal/http"
	"prac_04/services/tasks/internal/service"
	"prac_04/shared/logger"
	"prac_04/shared/metrics"
	"prac_04/shared/middleware"
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

	reg := prometheus.NewRegistry()
	reg.MustRegister(prometheus.NewGoCollector())
	reg.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	httpMetrics := metrics.NewHTTPMetrics("tasks", reg)

	mux := http.NewServeMux()
	handler := taskshttp.NewHandler(service.NewTaskService(), authClient, log)
	handler.Register(mux)
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))

	wrapped := middleware.RequestID(
		middleware.Metrics(httpMetrics, middleware.TasksRouteClassifier)(
			middleware.AccessLog(log)(mux),
		),
	)

	addr := ":" + port
	log.WithField("port", port).WithField("auth_grpc", authGRPCAddr).Info("server started")
	if err := http.ListenAndServe(addr, wrapped); err != nil {
		log.WithError(err).Fatal("server error")
	}
}
