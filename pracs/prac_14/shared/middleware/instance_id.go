package middleware

import "net/http"

// HeaderInstanceID — заголовок, по которому видно, какая реплика обработала запрос.
const HeaderInstanceID = "X-Instance-ID"

// InstanceID добавляет на каждый ответ заголовок X-Instance-ID со значением
// идентификатора реплики (обычно из env INSTANCE_ID, напр. tasks-1 / tasks-2).
//
// Это нужно для наблюдаемости при горизонтальном масштабировании: за
// балансировщиком (NGINX) стоят несколько одинаковых инстансов, и по этому
// заголовку в ответе curl видно, какой именно инстанс ответил, то есть что
// трафик реально распределяется. Заголовок ставится до вызова next, поэтому
// попадает в любой ответ, включая /health и ошибки авторизации.
//
// Если id пустой, заголовок не выставляется.
func InstanceID(id string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if id != "" {
				w.Header().Set(HeaderInstanceID, id)
			}
			next.ServeHTTP(w, r)
		})
	}
}
