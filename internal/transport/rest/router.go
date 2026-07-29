package rest

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	_ "sobes_stackbridge_go/docs" // сгенерированная swagger-документация
)

// requestTimeout ограничивает обработку запроса целиком, включая поход в БД:
// без него зависший запрос удерживал бы соединение из пула бессрочно.
const requestTimeout = 10 * time.Second

// NewRouter собирает маршруты сервиса.
func NewRouter(h *Handler, log *slog.Logger) http.Handler {
	router := chi.NewRouter()

	router.Use(middleware.RequestID)
	router.Use(h.recoverer)
	router.Use(requestLogger(log))
	router.Use(middleware.Timeout(requestTimeout))

	// Чтобы ошибки маршрутизации тоже приходили в JSON, а не текстом.
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		h.writeJSON(w, r, http.StatusNotFound, ErrorResponse{Code: "not_found", Message: "маршрут не найден"})
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		h.writeJSON(w, r, http.StatusMethodNotAllowed, ErrorResponse{
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

// recoverer перехватывает панику и отвечает тем же форматом ошибки, что и
// остальной API. Штатный chi middleware.Recoverer отдаёт пустой 500, из-за
// чего клиент, разбирающий code/message, ломался бы именно в аварии.
func (h *Handler) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}

			// ErrAbortHandler — штатный способ оборвать соединение,
			// его положено пробрасывать дальше.
			if recovered == http.ErrAbortHandler {
				panic(recovered)
			}

			h.logger(r).Error("паника при обработке запроса",
				slog.Any("panic", recovered),
				slog.String("stack", string(debug.Stack())),
			)

			h.writeJSON(w, r, http.StatusInternalServerError, ErrorResponse{
				Code:    "internal_error",
				Message: "внутренняя ошибка сервиса",
			})
		}()

		next.ServeHTTP(w, r)
	})
}

// requestLogger пишет строку на каждый обработанный запрос.
func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(wrapped, r)

			log.Info("запрос обработан",
				slog.String("request_id", middleware.GetReqID(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("query", r.URL.RawQuery),
				slog.Int("status", wrapped.Status()),
				slog.Duration("duration", time.Since(start)),
			)
		})
	}
}
