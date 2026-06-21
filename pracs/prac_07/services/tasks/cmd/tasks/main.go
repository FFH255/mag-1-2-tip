package main

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	tasksauth "prac_07/services/tasks/internal/client/authclient"
	taskshttp "prac_07/services/tasks/internal/http"
	"prac_07/services/tasks/internal/repository"
	"prac_07/services/tasks/internal/service"
	"prac_07/shared/logger"
	"prac_07/shared/metrics"
	"prac_07/shared/middleware"
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

	dsn := os.Getenv("TASKS_DB_DSN")
	if dsn == "" {
		dsn = "postgres://tasks:tasks@localhost:5432/tasks?sslmode=disable"
	}

	dbCtx, dbCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dbCancel()

	pool, err := pgxpool.New(dbCtx, dsn)
	if err != nil {
		log.WithError(err).Fatal("failed to create db pool")
	}
	defer pool.Close()

	if err := pool.Ping(dbCtx); err != nil {
		log.WithError(err).Fatal("failed to ping db")
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

	repo := repository.NewPostgresRepository(pool)
	taskService := service.NewTaskService(repo)

	mux := http.NewServeMux()
	handler := taskshttp.NewHandler(taskService, authClient, log)
	handler.Register(mux)
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))

	wrapped := middleware.RequestID(
		middleware.SecurityHeaders(
			middleware.Metrics(httpMetrics, middleware.TasksRouteClassifier)(
				middleware.AccessLog(log)(
					middleware.CSRF(log)(mux),
				),
			),
		),
	)

	addr := ":" + port
	log.WithField("port", port).WithField("auth_grpc", authGRPCAddr).Info("server started")
	if err := http.ListenAndServe(addr, wrapped); err != nil {
		log.WithError(err).Fatal("server error")
	}
}
