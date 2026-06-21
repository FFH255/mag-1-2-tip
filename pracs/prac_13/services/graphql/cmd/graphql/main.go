package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"

	"prac_13/services/graphql/graph"
	"prac_13/services/graphql/graph/generated"
	"prac_13/services/graphql/internal/auth"
	"prac_13/services/graphql/internal/repository"
	"prac_13/services/graphql/internal/service"
	"prac_13/shared/httpx"
	"prac_13/shared/logger"
	"prac_13/shared/middleware"
)

func main() {
	log := logger.New("graphql")

	port := os.Getenv("GRAPHQL_PORT")
	if port == "" {
		port = "8090"
	}

	// Слой данных. Если задан TASKS_DB_DSN — ходим в ту же таблицу tasks, что и
	// REST-сервис (единый источник истины). Иначе — in-memory, чтобы сервис
	// поднимался и проверялся в Playground без PostgreSQL.
	repo, closeRepo := buildRepository(log)
	defer closeRepo()

	svc := service.NewTaskService(repo)
	resolver := &graph.Resolver{TaskSvc: svc}

	srv := handler.NewDefaultServer(generated.NewExecutableSchema(
		generated.Config{Resolvers: resolver},
	))

	// Упрощённая авторизация (раздел 6 методички). По умолчанию ОТКЛЮЧЕНА, чтобы
	// сфокусироваться на механике GraphQL. Включается GRAPHQL_REQUIRE_AUTH=true:
	// тогда мутации требуют валидный Bearer-токен (проверка — gRPC Auth.Verify),
	// а запросы (Query) остаются открытыми.
	httpHandler := http.Handler(srv)
	if requireAuth() {
		authAddr := envOr("AUTH_GRPC_ADDR", "localhost:50051")
		verifier, err := auth.NewGRPCVerifier(authAddr)
		if err != nil {
			log.WithError(err).Fatal("failed to init auth verifier")
		}
		defer verifier.Close()

		srv.AroundOperations(auth.RequireForMutations(verifier, log))
		httpHandler = auth.HTTPMiddleware(srv)
		log.WithField("auth_grpc", authAddr).Info("mutation authorization ENABLED (Bearer token required)")
	} else {
		log.Warn("authorization is DISABLED (GRAPHQL_REQUIRE_AUTH != true): all operations are open")
	}

	mux := http.NewServeMux()
	// Playground (GraphiQL) на "/", сами запросы шлются POST-ом на "/query".
	mux.Handle("/", playground.Handler("Tasks GraphQL", "/query"))
	mux.Handle("/query", httpHandler)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	wrapped := middleware.RequestID(middleware.SecurityHeaders(middleware.AccessLog(log)(mux)))

	log.WithField("port", port).
		WithField("playground", "http://localhost:"+port+"/").
		WithField("endpoint", "/query").
		Info("server started")
	if err := http.ListenAndServe(":"+port, wrapped); err != nil {
		log.WithError(err).Fatal("server error")
	}
}

// buildRepository выбирает реализацию хранилища и возвращает функцию закрытия.
func buildRepository(log *logrus.Entry) (service.TaskRepository, func()) {
	dsn := strings.TrimSpace(os.Getenv("TASKS_DB_DSN"))
	if dsn == "" {
		log.Warn("TASKS_DB_DSN is not set: using in-memory repository (data is not persisted)")
		mem := repository.NewMemoryRepository()
		mem.Seed(seedTasks()...)
		return mem, func() {}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.WithError(err).Fatal("failed to create db pool")
	}
	if err := pool.Ping(ctx); err != nil {
		log.WithError(err).Fatal("failed to ping db")
	}
	log.Info("using PostgreSQL repository (shared 'tasks' table with REST service)")
	return repository.NewPostgresRepository(pool), pool.Close
}

// seedTasks — стартовые данные для in-memory режима. id "t_001" совпадает с
// примерами из методички, чтобы запросы из раздела 8 работали сразу.
func seedTasks() []service.Task {
	return []service.Task{
		{ID: "t_001", Title: "First task", Description: "seeded sample", Done: false},
		{ID: "t_002", Title: "Done task", Description: "already finished", Done: true},
	}
}

func requireAuth() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GRAPHQL_REQUIRE_AUTH"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
