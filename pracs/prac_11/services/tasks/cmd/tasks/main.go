package main

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"

	"prac_11/services/tasks/internal/cache"
	tasksauth "prac_11/services/tasks/internal/client/authclient"
	taskshttp "prac_11/services/tasks/internal/http"
	"prac_11/services/tasks/internal/repository"
	"prac_11/services/tasks/internal/service"
	"prac_11/shared/logger"
	"prac_11/shared/metrics"
	"prac_11/shared/middleware"
)

func main() {
	log := logger.New("tasks")

	port := os.Getenv("TASKS_PORT")
	if port == "" {
		port = "8086"
	}

	// Идентификатор реплики для горизонтального масштабирования (ПЗ №10).
	// За балансировщиком стоит несколько одинаковых инстансов tasks; по этому
	// значению (заголовок X-Instance-ID) видно, какой из них ответил. Если env
	// не задан, подставляем hostname контейнера как разумный фолбэк.
	instanceID := os.Getenv("INSTANCE_ID")
	if instanceID == "" {
		if hn, err := os.Hostname(); err == nil {
			instanceID = hn
		}
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

	// Кэш — необязательная инфраструктурная зависимость. Если REDIS_ADDR не
	// задан, используем заглушку (NopCache) и работаем напрямую с БД. Даже
	// когда адрес задан, недоступность Redis на старте не фатальна: соединение
	// ленивое, а операции деградируют до промахов.
	taskCache := buildCache(log)
	if closer, ok := taskCache.(interface{ Close() error }); ok {
		defer closer.Close()
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(prometheus.NewGoCollector())
	reg.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	httpMetrics := metrics.NewHTTPMetrics("tasks", reg)

	repo := repository.NewPostgresRepository(pool)
	taskService := service.NewTaskService(repo, taskCache)

	mux := http.NewServeMux()
	handler := taskshttp.NewHandler(taskService, authClient, log)
	handler.Register(mux)
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))

	wrapped := middleware.RequestID(
		middleware.InstanceID(instanceID)(
			middleware.SecurityHeaders(
				middleware.Metrics(httpMetrics, middleware.TasksRouteClassifier)(
					middleware.AccessLog(log)(
						middleware.CSRF(log)(mux),
					),
				),
			),
		),
	)

	addr := ":" + port
	log.WithField("port", port).
		WithField("instance_id", instanceID).
		WithField("auth_grpc", authGRPCAddr).
		Info("server started")
	if err := http.ListenAndServe(addr, wrapped); err != nil {
		log.WithError(err).Fatal("server error")
	}
}

// buildCache собирает реализацию кэша из переменных окружения:
//
//	REDIS_ADDR                — адрес(а) Redis; пусто → кэш отключён (NopCache).
//	REDIS_PASSWORD            — пароль (если задан).
//	REDIS_DB                  — номер БД для standalone (по умолчанию 0).
//	CACHE_TTL_SECONDS         — базовый TTL записи (по умолчанию 120).
//	CACHE_TTL_JITTER_SECONDS  — макс. случайный разброс TTL (по умолчанию 30).
func buildCache(log *logrus.Entry) service.TaskCache {
	addrs := cache.ParseAddrs(os.Getenv("REDIS_ADDR"))
	if len(addrs) == 0 {
		log.Warn("REDIS_ADDR is not set, caching is disabled (DB-only mode)")
		return cache.NewNop()
	}

	ttl := time.Duration(envInt("CACHE_TTL_SECONDS", 120)) * time.Second
	jitter := time.Duration(envInt("CACHE_TTL_JITTER_SECONDS", 30)) * time.Second

	rc := cache.NewRedis(cache.Config{
		Addrs:    addrs,
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       envInt("REDIS_DB", 0),
		TTL:      ttl,
		Jitter:   jitter,
	}, log)

	// Best-effort проверка: при ошибке только предупреждаем — сервис стартует
	// и в DB-only режиме (деградация), кэш подхватится, когда Redis оживёт.
	pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rc.Ping(pingCtx); err != nil {
		log.WithError(err).Warn("redis ping failed on startup, continuing in degraded (DB-only) mode")
	} else {
		log.WithField("redis_addr", addrs).WithField("ttl", ttl).Info("redis cache enabled")
	}
	return rc
}

// envInt читает целочисленную переменную окружения с значением по умолчанию.
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
