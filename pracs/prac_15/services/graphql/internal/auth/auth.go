// Package auth — упрощённая авторизация для GraphQL (ПЗ №11, раздел 6).
//
// Идея: HTTP-слой кладёт значение заголовка Authorization в контекст запроса, а
// операционный middleware gqlgen ПЕРЕД выполнением резолверов проверяет токен.
// Проверка включается только для мутаций (Query остаются открытыми) и только
// когда авторизация включена через переменную окружения. Токен проверяет тот
// же Auth-сервис (gRPC Verify), что и REST-сервис tasks.
package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/sirupsen/logrus"
	"github.com/vektah/gqlparser/v2/ast"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	authv1 "prac_15/gen/auth/v1"
)

// ErrUnauthorized — токен отсутствует или невалиден.
var ErrUnauthorized = errors.New("unauthorized")

type ctxKey int

const tokenKey ctxKey = iota

// HTTPMiddleware извлекает bearer-токен из заголовка Authorization и кладёт его
// в контекст. Сам по себе ничего не запрещает — решение принимает операционный
// middleware, который видит тип операции (Query/Mutation).
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r.Header.Get("Authorization"))
		ctx := context.WithValue(r.Context(), tokenKey, token)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func tokenFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(tokenKey).(string); ok {
		return v
	}
	return ""
}

// Verifier проверяет токен. Реализация по умолчанию — GRPCVerifier.
type Verifier interface {
	Verify(ctx context.Context, token string) error
}

// RequireForMutations — операционный middleware gqlgen. Для операций типа
// Mutation требует валидный токен; Query пропускает без проверки. Срабатывает
// ДО резолверов, поэтому неавторизованная мутация не доходит до бизнес-логики.
func RequireForMutations(v Verifier, log *logrus.Entry) graphql.OperationMiddleware {
	return func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		oc := graphql.GetOperationContext(ctx)
		if oc.Operation != nil && oc.Operation.Operation == ast.Mutation {
			if err := v.Verify(ctx, tokenFromContext(ctx)); err != nil {
				log.WithError(err).Warn("graphql mutation rejected: unauthorized")
				return graphql.OneShot(graphql.ErrorResponse(ctx,
					"unauthorized: valid Bearer token is required for mutations"))
			}
		}
		return next(ctx)
	}
}

// GRPCVerifier проверяет токен через gRPC Auth.Verify — тот же сервис auth, что
// используется в REST. Так GraphQL и REST доверяют одному источнику правды об
// аутентификации.
type GRPCVerifier struct {
	conn    *grpc.ClientConn
	client  authv1.AuthServiceClient
	timeout time.Duration
}

func NewGRPCVerifier(addr string) (*GRPCVerifier, error) {
	conn, err := grpc.NewClient(
		strings.TrimSpace(addr),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, err
	}
	return &GRPCVerifier{
		conn:    conn,
		client:  authv1.NewAuthServiceClient(conn),
		timeout: 2 * time.Second,
	}, nil
}

func (v *GRPCVerifier) Close() error {
	if v == nil || v.conn == nil {
		return nil
	}
	return v.conn.Close()
}

func (v *GRPCVerifier) Verify(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return ErrUnauthorized
	}
	ctx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()

	resp, err := v.client.Verify(ctx, &authv1.VerifyRequest{Token: token})
	if err != nil {
		return err
	}
	if !resp.GetValid() {
		return ErrUnauthorized
	}
	return nil
}

// bearerToken достаёт токен из строки "Bearer <token>" (регистр схемы не важен).
func bearerToken(authorization string) string {
	const prefix = "bearer "
	if len(authorization) < len(prefix) || !strings.EqualFold(authorization[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(authorization[len(prefix):])
}
