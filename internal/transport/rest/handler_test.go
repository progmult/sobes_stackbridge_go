package rest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"sobes_stackbridge_go/internal/model"
	"sobes_stackbridge_go/internal/service"
	"sobes_stackbridge_go/internal/transport/rest"
)

const userID = "60601fee-2bf1-4721-ae6f-7636e79a0cba"

// repoStub — заглушка хранилища: база данных для этих тестов не нужна.
type repoStub struct {
	subscription *model.Subscription
	page         model.Page
	notFound     bool
	panics       bool
	err          error
}

func (r *repoStub) Create(_ context.Context, sub *model.Subscription) (*model.Subscription, error) {
	sub.ID = uuid.MustParse("7c6f1e2a-6b4e-4c67-9f3f-2f2a1d6b8c11")
	r.subscription = sub

	return sub, nil
}

func (r *repoStub) GetByID(_ context.Context, id uuid.UUID) (*model.Subscription, error) {
	if r.panics {
		panic("сбой в хранилище")
	}

	if r.err != nil {
		return nil, r.err
	}

	if r.notFound {
		return nil, model.ErrNotFound
	}

	return &model.Subscription{
		ID:          id,
		ServiceName: "Yandex Plus",
		Price:       400,
		UserID:      uuid.MustParse(userID),
		StartDate:   time.Date(2025, time.July, 1, 0, 0, 0, 0, time.UTC),
	}, nil
}

func (r *repoStub) Update(_ context.Context, sub *model.Subscription) (*model.Subscription, error) {
	if r.notFound {
		return nil, model.ErrNotFound
	}

	r.subscription = sub

	return sub, nil
}

func (r *repoStub) Delete(context.Context, uuid.UUID) error {
	if r.notFound {
		return model.ErrNotFound
	}

	return nil
}

func (r *repoStub) List(_ context.Context, _ model.Filter, page model.Page) ([]model.Subscription, int, error) {
	r.page = page

	return nil, 0, nil
}

func (r *repoStub) SumForPeriod(context.Context, time.Time, time.Time, model.Filter) (int64, error) {
	return 4800, nil
}

// pingerStub изображает доступную или упавшую базу.
type pingerStub struct{ err error }

func (p pingerStub) Ping(context.Context) error { return p.err }

func newRouter(repo *repoStub, pinger rest.Pinger) http.Handler {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	return rest.NewRouter(rest.NewHandler(service.New(repo, log), pinger, log), log)
}

func do(t *testing.T, router http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	return recorder
}

// body собирает корректное тело запроса, подменяя одно поле.
func body(field, value string) string {
	fields := map[string]string{
		"service_name": `"Yandex Plus"`,
		"price":        `400`,
		"user_id":      `"` + userID + `"`,
		"start_date":   `"07-2025"`,
	}
	fields[field] = value

	parts := make([]string, 0, len(fields))
	for _, name := range []string{"service_name", "price", "user_id", "start_date"} {
		parts = append(parts, `"`+name+`":`+fields[name])
	}

	return "{" + strings.Join(parts, ",") + "}"
}

func TestCreateRejectsBadRequests(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "отрицательная стоимость", body: body("price", `-1`)},
		{name: "стоимость больше INTEGER", body: body("price", `3000000000`)},
		{name: "стоимость не указана", body: `{"service_name":"X","user_id":"` + userID + `","start_date":"07-2025"}`},
		{name: "название длиннее 255 символов", body: body("service_name", `"`+strings.Repeat("a", 256)+`"`)},
		{name: "пустое название", body: body("service_name", `"   "`)},
		{name: "некорректный user_id", body: body("user_id", `"не-uuid"`)},
		{name: "дата в чужом формате", body: body("start_date", `"2025-07"`)},
		{name: "дата окончания раньше начала", body: `{"service_name":"X","price":1,"user_id":"` + userID + `","start_date":"07-2025","end_date":"01-2025"}`},
		{name: "битый JSON", body: `{`},
		{name: "мусор после JSON-объекта", body: body("price", `400`) + ` THIS IS NOT JSON`},
		{name: "неизвестное поле", body: `{"service_name":"X","price":1,"user_id":"` + userID + `","start_date":"07-2025","end_data":"12-2025"}`},
		{name: "пустое тело", body: ``},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := do(t, newRouter(&repoStub{}, pingerStub{}), http.MethodPost, "/api/v1/subscriptions", tt.body)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("код ответа = %d, ожидался 400; тело: %s", resp.Code, resp.Body.String())
			}

			var payload rest.ErrorResponse
			if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
				t.Fatalf("ответ не разобрался как JSON: %v", err)
			}

			if payload.Code != "validation_error" {
				t.Errorf("code = %q, ожидался validation_error", payload.Code)
			}

			if payload.Message == "" {
				t.Error("message пустой")
			}
		})
	}
}

// TestCreateReportsAllViolations закрепляет, что проверка не обрывается на
// первой ошибке: клиент должен увидеть все проблемные поля за один запрос.
func TestCreateReportsAllViolations(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "нарушены инварианты записи",
			body: `{"service_name":"","price":-1,"user_id":"` + uuid.Nil.String() + `","start_date":"07-2025","end_date":"01-2025"}`,
			want: []string{
				"название сервиса не может быть пустым",
				"стоимость подписки не может быть отрицательной",
				"не указан идентификатор пользователя",
				"дата окончания не может быть раньше даты начала",
			},
		},
		{
			name: "не разобрались несколько полей",
			body: `{"service_name":"X","user_id":"не-uuid","start_date":"2025-07","end_date":"тоже-не-дата"}`,
			want: []string{
				"не указана стоимость подписки",
				"user_id должен быть корректным UUID",
				"дата начала должна быть в формате MM-YYYY, например 07-2025",
				"дата окончания должна быть в формате MM-YYYY, например 12-2025",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := do(t, newRouter(&repoStub{}, pingerStub{}), http.MethodPost, "/api/v1/subscriptions", tt.body)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("код ответа = %d, ожидался 400; тело: %s", resp.Code, resp.Body.String())
			}

			var payload rest.ErrorResponse
			if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
				t.Fatalf("ответ не разобрался как JSON: %v", err)
			}

			if len(payload.Details) != len(tt.want) {
				t.Fatalf("details = %q, ожидалось %d нарушений", payload.Details, len(tt.want))
			}

			for i, want := range tt.want {
				if payload.Details[i] != want {
					t.Errorf("details[%d] = %q, ожидалось %q", i, payload.Details[i], want)
				}
			}

			// Тот же перечень должен читаться и из message — клиентом, который
			// про details не знает.
			for _, want := range tt.want {
				if !strings.Contains(payload.Message, want) {
					t.Errorf("в message нет нарушения %q: %s", want, payload.Message)
				}
			}
		})
	}
}

// TestDetailsOnlyForBody разграничивает две ситуации: перечень нарушений
// сопровождает проверку тела запроса, а у ошибок в query-параметрах его нет —
// там нарушение всегда одно и целиком помещается в message.
func TestDetailsOnlyForBody(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		target      string
		body        string
		wantDetails int
	}{
		{
			name:   "одно нарушение в теле",
			method: http.MethodPost, target: "/api/v1/subscriptions", body: body("price", `-1`),
			wantDetails: 1,
		},
		{
			name:   "ошибка в query-параметре",
			method: http.MethodGet, target: "/api/v1/subscriptions?limit=0",
			wantDetails: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := do(t, newRouter(&repoStub{}, pingerStub{}), tt.method, tt.target, tt.body)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("код ответа = %d, ожидался 400; тело: %s", resp.Code, resp.Body.String())
			}

			var payload rest.ErrorResponse
			if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
				t.Fatalf("ответ не разобрался как JSON: %v", err)
			}

			if len(payload.Details) != tt.wantDetails {
				t.Errorf("details = %q, ожидалось %d", payload.Details, tt.wantDetails)
			}

			if payload.Message == "" {
				t.Error("message пустой")
			}
		})
	}
}

func TestCreateSuccess(t *testing.T) {
	repo := &repoStub{}
	resp := do(t, newRouter(repo, pingerStub{}), http.MethodPost, "/api/v1/subscriptions", body("price", `400`))

	if resp.Code != http.StatusCreated {
		t.Fatalf("код ответа = %d, ожидался 201; тело: %s", resp.Code, resp.Body.String())
	}

	if location := resp.Header().Get("Location"); location == "" {
		t.Error("в ответе нет заголовка Location")
	}

	var payload rest.SubscriptionResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("ответ не разобрался как JSON: %v", err)
	}

	if payload.StartDate != "07-2025" {
		t.Errorf("start_date = %q, ожидалось 07-2025", payload.StartDate)
	}

	if repo.subscription.ServiceName != "Yandex Plus" {
		t.Errorf("в хранилище ушло название %q", repo.subscription.ServiceName)
	}
}

func TestRoutingAndNotFound(t *testing.T) {
	tests := []struct {
		name   string
		method string
		target string
		status int
		code   string
	}{
		{name: "подписки нет", method: http.MethodGet, target: "/api/v1/subscriptions/" + uuid.Nil.String(), status: http.StatusNotFound, code: "not_found"},
		{name: "id не uuid", method: http.MethodGet, target: "/api/v1/subscriptions/хлам", status: http.StatusBadRequest, code: "validation_error"},
		{name: "неизвестный маршрут", method: http.MethodGet, target: "/api/v1/нет-такого", status: http.StatusNotFound, code: "not_found"},
		{name: "метод не поддерживается", method: http.MethodPatch, target: "/api/v1/subscriptions", status: http.StatusMethodNotAllowed, code: "method_not_allowed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := do(t, newRouter(&repoStub{notFound: true}, pingerStub{}), tt.method, tt.target, "")

			if resp.Code != tt.status {
				t.Fatalf("код ответа = %d, ожидался %d; тело: %s", resp.Code, tt.status, resp.Body.String())
			}

			var payload rest.ErrorResponse
			if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
				t.Fatalf("ответ не разобрался как JSON: %v", err)
			}

			if payload.Code != tt.code {
				t.Errorf("code = %q, ожидался %q", payload.Code, tt.code)
			}
		})
	}
}

func TestListPagination(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantLimit  int
		wantOffset int
	}{
		{name: "по умолчанию", query: "", wantStatus: http.StatusOK, wantLimit: rest.DefaultLimit},
		{name: "заданные значения", query: "?limit=10&offset=20", wantStatus: http.StatusOK, wantLimit: 10, wantOffset: 20},
		{name: "limit урезается до максимума", query: "?limit=9999", wantStatus: http.StatusOK, wantLimit: rest.MaxLimit},
		{name: "limit=0 отвергается", query: "?limit=0", wantStatus: http.StatusBadRequest},
		{name: "limit не число", query: "?limit=abc", wantStatus: http.StatusBadRequest},
		{name: "отрицательный offset", query: "?offset=-1", wantStatus: http.StatusBadRequest},
		{name: "некорректный user_id", query: "?user_id=хлам", wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &repoStub{}
			resp := do(t, newRouter(repo, pingerStub{}), http.MethodGet, "/api/v1/subscriptions"+tt.query, "")

			if resp.Code != tt.wantStatus {
				t.Fatalf("код ответа = %d, ожидался %d; тело: %s", resp.Code, tt.wantStatus, resp.Body.String())
			}

			if tt.wantStatus != http.StatusOK {
				return
			}

			if repo.page.Limit != tt.wantLimit || repo.page.Offset != tt.wantOffset {
				t.Errorf("в хранилище ушло limit=%d offset=%d, ожидалось limit=%d offset=%d",
					repo.page.Limit, repo.page.Offset, tt.wantLimit, tt.wantOffset)
			}

			// Пустая страница должна быть [], а не null.
			if !strings.Contains(resp.Body.String(), `"items":[]`) {
				t.Errorf("пустой список отдан как %s", resp.Body.String())
			}
		})
	}
}

func TestSummaryRequiresValidPeriod(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantStatus int
	}{
		{name: "корректный период", query: "?from=01-2025&to=12-2025", wantStatus: http.StatusOK},
		{name: "from не задан", query: "?to=12-2025", wantStatus: http.StatusBadRequest},
		{name: "to не задан", query: "?from=01-2025", wantStatus: http.StatusBadRequest},
		{name: "период вывернут", query: "?from=12-2025&to=01-2025", wantStatus: http.StatusBadRequest},
		{name: "дата в чужом формате", query: "?from=2025-01&to=12-2025", wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := do(t, newRouter(&repoStub{}, pingerStub{}), http.MethodGet, "/api/v1/subscriptions/summary"+tt.query, "")

			if resp.Code != tt.wantStatus {
				t.Fatalf("код ответа = %d, ожидался %d; тело: %s", resp.Code, tt.wantStatus, resp.Body.String())
			}
		})
	}
}

// Сообщение клиенту не должно содержать внутренних деталей Go: имён структур,
// их полей, внутренних типов и английского текста стандартной библиотеки.
func TestErrorMessagesHideInternals(t *testing.T) {
	leaks := []string{"SubscriptionRequest", "json:", "http:", "Go struct", "SQLSTATE"}

	tests := []struct {
		name string
		body string
	}{
		{name: "неверный тип поля", body: `{"service_name":"X","price":"четыреста","user_id":"` + userID + `","start_date":"07-2025"}`},
		{name: "неизвестное поле", body: `{"service_name":"X","price":1,"user_id":"` + userID + `","start_date":"07-2025","end_data":"12-2025"}`},
		{name: "битый JSON", body: `{"service_name":`},
		{name: "мусор после объекта", body: body("price", `400`) + ` МУСОР`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := do(t, newRouter(&repoStub{}, pingerStub{}), http.MethodPost, "/api/v1/subscriptions", tt.body)

			var payload rest.ErrorResponse
			if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
				t.Fatalf("ответ не разобрался как JSON: %v", err)
			}

			for _, leak := range leaks {
				if strings.Contains(payload.Message, leak) {
					t.Errorf("в сообщение утекло %q: %s", leak, payload.Message)
				}
			}
		})
	}
}

// Санировать сообщение клиенту — не значит терять диагноз: исходная ошибка
// разбора обязана дойти до лога, иначе причину сбоя не восстановить.
func TestDecodeErrorCauseReachesLog(t *testing.T) {
	var logged bytes.Buffer

	log := slog.New(slog.NewJSONHandler(&logged, nil))
	router := rest.NewRouter(rest.NewHandler(service.New(&repoStub{}, log), pingerStub{}, log), log)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions",
		strings.NewReader(`{"service_name":"X","price":"четыреста","user_id":"`+userID+`","start_date":"07-2025"}`))
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	var payload rest.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("ответ не разобрался как JSON: %v", err)
	}

	if strings.Contains(payload.Message, "json:") {
		t.Errorf("исходная ошибка утекла клиенту: %s", payload.Message)
	}

	if !strings.Contains(logged.String(), "json:") {
		t.Errorf("исходной ошибки нет в логе: %s", logged.String())
	}

	if !strings.Contains(logged.String(), `"cause"`) {
		t.Errorf("причина записана не отдельным атрибутом: %s", logged.String())
	}
}

// Тело сверх лимита — это не ошибка данных, клиент должен отличать
// «уменьши тело» от «исправь поля».
func TestBodyOverLimitReturns413(t *testing.T) {
	huge := `{"service_name":"` + strings.Repeat("a", 2<<20) + `","price":1}`

	resp := do(t, newRouter(&repoStub{}, pingerStub{}), http.MethodPost, "/api/v1/subscriptions", huge)

	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("код ответа = %d, ожидался 413; тело: %s", resp.Code, resp.Body.String())
	}

	var payload rest.ErrorResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("ответ не разобрался как JSON: %v", err)
	}

	if payload.Code != "payload_too_large" {
		t.Errorf("code = %q, ожидался payload_too_large", payload.Code)
	}
}

func TestContentTypeIsChecked(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		wantStatus  int
	}{
		{name: "application/json", contentType: "application/json", wantStatus: http.StatusCreated},
		{name: "с параметром charset", contentType: "application/json; charset=utf-8", wantStatus: http.StatusCreated},
		{name: "заголовка нет", contentType: "", wantStatus: http.StatusCreated},
		{name: "чужой тип", contentType: "text/plain", wantStatus: http.StatusUnsupportedMediaType},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions", strings.NewReader(body("price", `400`)))
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}

			recorder := httptest.NewRecorder()
			newRouter(&repoStub{}, pingerStub{}).ServeHTTP(recorder, req)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("код ответа = %d, ожидался %d; тело: %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
		})
	}
}

// Отмена контекста — не отказ сервиса: истёкший таймаут это 504,
// а оборванное клиентом соединение вообще не повод отвечать ошибкой.
func TestTimeoutReturns504(t *testing.T) {
	repo := &repoStub{err: context.DeadlineExceeded}

	resp := do(t, newRouter(repo, pingerStub{}), http.MethodGet, "/api/v1/subscriptions/"+uuid.Nil.String(), "")

	if resp.Code != http.StatusGatewayTimeout {
		t.Fatalf("код ответа = %d, ожидался 504; тело: %s", resp.Code, resp.Body.String())
	}

	var payload rest.ErrorResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("ответ не разобрался как JSON: %v", err)
	}

	if payload.Code != "timeout" {
		t.Errorf("code = %q, ожидался timeout", payload.Code)
	}
}

func TestDeleteAndUpdate(t *testing.T) {
	tests := []struct {
		name       string
		notFound   bool
		method     string
		body       string
		wantStatus int
	}{
		{name: "удаление существующей", method: http.MethodDelete, wantStatus: http.StatusNoContent},
		{name: "удаление несуществующей", notFound: true, method: http.MethodDelete, wantStatus: http.StatusNotFound},
		{name: "обновление существующей", method: http.MethodPut, body: body("price", `500`), wantStatus: http.StatusOK},
		{name: "обновление несуществующей", notFound: true, method: http.MethodPut, body: body("price", `500`), wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := newRouter(&repoStub{notFound: tt.notFound}, pingerStub{})
			resp := do(t, router, tt.method, "/api/v1/subscriptions/"+uuid.Nil.String(), tt.body)

			if resp.Code != tt.wantStatus {
				t.Fatalf("код ответа = %d, ожидался %d; тело: %s", resp.Code, tt.wantStatus, resp.Body.String())
			}

			if tt.wantStatus == http.StatusNoContent && resp.Body.Len() != 0 {
				t.Errorf("204 должен быть без тела, получено: %s", resp.Body.String())
			}
		})
	}
}

// Паника не должна нарушать контракт: клиент, разбирающий code и message,
// не может ломаться именно в аварийном сценарии.
func TestPanicReturnsSameErrorFormat(t *testing.T) {
	router := newRouter(&repoStub{panics: true}, pingerStub{})
	resp := do(t, router, http.MethodGet, "/api/v1/subscriptions/"+uuid.Nil.String(), "")

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("код ответа = %d, ожидался 500", resp.Code)
	}

	var payload rest.ErrorResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("ответ не разобрался как JSON: %v", err)
	}

	if payload.Code != "internal_error" {
		t.Errorf("code = %q, ожидался internal_error", payload.Code)
	}

	if strings.Contains(payload.Message, "сбой в хранилище") {
		t.Error("детали паники утекли клиенту")
	}
}

// Упавший запрос обязан попасть в лог доступа: иначе паники невидимы в
// статистике статусов и латентности — ровно там, где важнее всего. Тест
// закрепляет порядок middleware: логгер снаружи recoverer.
func TestPanicIsRecordedInAccessLog(t *testing.T) {
	var logged bytes.Buffer

	log := slog.New(slog.NewJSONHandler(&logged, nil))
	router := rest.NewRouter(rest.NewHandler(service.New(&repoStub{panics: true}, log), pingerStub{}, log), log)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions/"+uuid.Nil.String(), nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if !strings.Contains(logged.String(), "запрос обработан") {
		t.Error("строки лога доступа для упавшего запроса нет")
	}

	if !strings.Contains(logged.String(), `"status":500`) {
		t.Errorf("в строке лога доступа не зафиксирован статус 500: %s", logged.String())
	}
}

func TestHealthReflectsDatabaseState(t *testing.T) {
	resp := do(t, newRouter(&repoStub{}, pingerStub{}), http.MethodGet, "/healthz", "")
	if resp.Code != http.StatusOK {
		t.Errorf("при доступной базе код = %d, ожидался 200", resp.Code)
	}

	resp = do(t, newRouter(&repoStub{}, pingerStub{err: context.DeadlineExceeded}), http.MethodGet, "/healthz", "")
	if resp.Code != http.StatusServiceUnavailable {
		t.Errorf("при недоступной базе код = %d, ожидался 503", resp.Code)
	}
}
