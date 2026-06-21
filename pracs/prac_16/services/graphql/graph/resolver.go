package graph

import "prac_16/services/graphql/internal/service"

// Resolver — корень резолверов. gqlgen прокидывает его в queryResolver /
// mutationResolver. Здесь хранятся зависимости, общие для всех резолверов.
//
// Бизнес-логика не дублируется: резолверы делегируют в тот же
// service.TaskService, что и REST-сервис tasks. Это и есть "единый слой
// данных" из методички — один источник истины для REST и GraphQL.
//
//go:generate go run github.com/99designs/gqlgen@v0.17.81 generate
type Resolver struct {
	// Имя поля намеренно не "Tasks": так оно не конфликтует со сгенерированным
	// методом-резолвером Tasks() для запроса tasks.
	TaskSvc *service.TaskService
}
