package rest

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	_ "sobes_stackbridge_go/docs" // сгенерированная swagger-документация
)

// NewRouter собирает маршруты сервиса.
func NewRouter(h *Handler, log *slog.Logger) http.Handler {
	router := chi.NewRouter()

	router.Use(middleware.Recoverer)
	router.Use(requestLogger(log))

	// Чтобы ошибки маршрутизации тоже приходили в JSON, а не текстом.
	router.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		h.writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "not_found", Message: "маршрут не найден"})
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		h.writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{
			Code:    "method_not_allowed",
			Message: "метод не поддерживается",
		})
	})

	router.Get("/healthz", h.Health)
	router.Get("/swagger/*", httpSwagger.Handler(httpSwagger.URL("/swagger/doc.json")))

	router.Route("/api/v1/subscriptions", func(r chi.Router) {
		r.Post("/", h.Create)
		r.Get("/", h.List)
		r.Get("/summary", h.Summary)
		r.Get("/{id}", h.GetByID)
		r.Put("/{id}", h.Update)
		r.Delete("/{id}", h.Delete)
	})

	return router
}

// requestLogger пишет строку на каждый обработанный запрос.
func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(wrapped, r)

			log.Info("запрос обработан",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("query", r.URL.RawQuery),
				slog.Int("status", wrapped.Status()),
				slog.Duration("duration", time.Since(start)),
			)
		})
	}
}
