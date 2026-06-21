package middleware

import "net/http"

// SecurityHeaders добавляет базовые заголовки безопасности на все ответы.
//
//   - X-Content-Type-Options: nosniff — запрещает браузеру угадывать Content-Type
//     и исполнять, например, JSON как JavaScript.
//   - Content-Security-Policy: default-src 'none' — JSON API не отдаёт HTML,
//     так что политика максимально строгая: ни скриптов, ни фреймов, ни ресурсов.
//   - X-Frame-Options: DENY — защита от clickjacking.
//   - Referrer-Policy: no-referrer — не утекает реферер во внешние сервисы.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
