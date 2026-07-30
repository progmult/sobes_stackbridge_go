// Package rest содержит HTTP-слой сервиса: маршруты, обработчики и DTO.
package rest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"sobes_stackbridge_go/internal/model"
	"sobes_stackbridge_go/internal/service"
)

const (
	// maxRequestBodySize ограничивает тело запроса, чтобы клиент не мог
	// израсходовать память сервиса одним запросом.
	maxRequestBodySize = 1 << 20 // 1 МБ
	// healthTimeout ограничивает проверку доступности базы.
	healthTimeout = 2 * time.Second
)

// Границы размера страницы — политика HTTP-слоя, а не доменный инвариант.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// Ошибки уровня протокола: домена они не касаются, поэтому живут здесь,
// а не в model.
var (
	errPayloadTooLarge      = errors.New("тело запроса превышает допустимый размер")
	errUnsupportedMediaType = errors.New("тело запроса должно быть в формате JSON")
	errRequestTimeout       = errors.New("превышено время обработки запроса")
)

// Pinger проверяет доступность базы данных. Ему удовлетворяет *pgxpool.Pool.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Handler обслуживает HTTP-запросы к подпискам.
type Handler struct {
	service *service.Service
	pinger  Pinger
	log     *slog.Logger
}

// NewHandler создаёт обработчик подписок.
func NewHandler(svc *service.Service, pinger Pinger, log *slog.Logger) *Handler {
	return &Handler{service: svc, pinger: pinger, log: log}
}

// Create godoc
//
//	@Summary		Создать подписку
//	@Description	Создаёт запись о подписке. Даты передаются в формате MM-YYYY.
//	@Tags			subscriptions
//	@Accept			json
//	@Produce		json
//	@Param			request	body		SubscriptionRequest	true	"Данные подписки"
//	@Success		201		{object}	SubscriptionResponse
//	@Failure		400		{object}	ErrorResponse
//	@Failure		413		{object}	ErrorResponse
//	@Failure		415		{object}	ErrorResponse
//	@Failure		500		{object}	ErrorResponse
//	@Failure		504		{object}	ErrorResponse
//	@Router			/subscriptions [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	req, err := decodeRequest(w, r)
	if err != nil {
		h.writeError(w, r, err)

		return
	}

	sub, err := req.toModel(uuid.Nil)
	if err != nil {
		h.writeError(w, r, err)

		return
	}

	created, err := h.service.Create(r.Context(), sub)
	if err != nil {
		h.writeError(w, r, err)

		return
	}

	// Location на 201 — требование REST: клиент узнаёт адрес созданного ресурса.
	w.Header().Set("Location", "/api/v1/subscriptions/"+created.ID.String())
	h.writeJSON(w, r, http.StatusCreated, newSubscriptionResponse(created))
}

// GetByID godoc
//
//	@Summary		Получить подписку
//	@Tags			subscriptions
//	@Produce		json
//	@Param			id	path		string	true	"ID подписки"	format(uuid)
//	@Success		200	{object}	SubscriptionResponse
//	@Failure		400	{object}	ErrorResponse
//	@Failure		404	{object}	ErrorResponse
//	@Failure		500	{object}	ErrorResponse
//	@Failure		504	{object}	ErrorResponse
//	@Router			/subscriptions/{id} [get]
func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, err := subscriptionID(r)
	if err != nil {
		h.writeError(w, r, err)

		return
	}

	sub, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)

		return
	}

	h.writeJSON(w, r, http.StatusOK, newSubscriptionResponse(sub))
}

// Update godoc
//
//	@Summary		Обновить подписку
//	@Tags			subscriptions
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string				true	"ID подписки"	format(uuid)
//	@Param			request	body		SubscriptionRequest	true	"Новые данные подписки"
//	@Success		200		{object}	SubscriptionResponse
//	@Failure		400		{object}	ErrorResponse
//	@Failure		404		{object}	ErrorResponse
//	@Failure		413		{object}	ErrorResponse
//	@Failure		415		{object}	ErrorResponse
//	@Failure		500		{object}	ErrorResponse
//	@Failure		504		{object}	ErrorResponse
//	@Router			/subscriptions/{id} [put]
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := subscriptionID(r)
	if err != nil {
		h.writeError(w, r, err)

		return
	}

	req, err := decodeRequest(w, r)
	if err != nil {
		h.writeError(w, r, err)

		return
	}

	sub, err := req.toModel(id)
	if err != nil {
		h.writeError(w, r, err)

		return
	}

	updated, err := h.service.Update(r.Context(), sub)
	if err != nil {
		h.writeError(w, r, err)

		return
	}

	h.writeJSON(w, r, http.StatusOK, newSubscriptionResponse(updated))
}

// Delete godoc
//
//	@Summary		Удалить подписку
//	@Tags			subscriptions
//	@Produce		json
//	@Param			id	path	string	true	"ID подписки"	format(uuid)
//	@Success		204	"Подписка удалена"
//	@Failure		400	{object}	ErrorResponse
//	@Failure		404	{object}	ErrorResponse
//	@Failure		500	{object}	ErrorResponse
//	@Failure		504	{object}	ErrorResponse
//	@Router			/subscriptions/{id} [delete]
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := subscriptionID(r)
	if err != nil {
		h.writeError(w, r, err)

		return
	}

	if err := h.service.Delete(r.Context(), id); err != nil {
		h.writeError(w, r, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// List godoc
//
//	@Summary		Список подписок
//	@Description	Возвращает подписки с необязательной фильтрацией по пользователю и названию сервиса.
//	@Tags			subscriptions
//	@Produce		json
//	@Param			user_id			query		string	false	"Фильтр по ID пользователя"	format(uuid)
//	@Param			service_name	query		string	false	"Фильтр по названию сервиса"
//	@Param			limit			query		int		false	"Размер страницы, от 1 до 200"	default(50)
//	@Param			offset			query		int		false	"Смещение от начала выборки"	default(0)
//	@Success		200				{object}	ListResponse
//	@Failure		400				{object}	ErrorResponse
//	@Failure		500				{object}	ErrorResponse
//	@Failure		504				{object}	ErrorResponse
//	@Router			/subscriptions [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	filter, err := parseFilter(r)
	if err != nil {
		h.writeError(w, r, err)

		return
	}

	page, err := parsePage(r)
	if err != nil {
		h.writeError(w, r, err)

		return
	}

	subscriptions, total, err := h.service.List(r.Context(), filter, page)
	if err != nil {
		h.writeError(w, r, err)

		return
	}

	items := make([]SubscriptionResponse, 0, len(subscriptions))
	for i := range subscriptions {
		items = append(items, newSubscriptionResponse(&subscriptions[i]))
	}

	h.writeJSON(w, r, http.StatusOK, ListResponse{
		Items:  items,
		Total:  total,
		Limit:  page.Limit,
		Offset: page.Offset,
	})
}

// Summary godoc
//
//	@Summary		Суммарная стоимость подписок
//	@Description	Считает суммарную стоимость подписок за период [from, to] включительно. Учитываются только месяцы, попадающие в период; подписка без даты окончания считается активной до конца периода.
//	@Tags			subscriptions
//	@Produce		json
//	@Param			from			query		string	true	"Начало периода, MM-YYYY"	example(01-2025)
//	@Param			to				query		string	true	"Конец периода, MM-YYYY"	example(12-2025)
//	@Param			user_id			query		string	false	"Фильтр по ID пользователя"	format(uuid)
//	@Param			service_name	query		string	false	"Фильтр по названию сервиса"
//	@Success		200				{object}	SummaryResponse
//	@Failure		400				{object}	ErrorResponse
//	@Failure		500				{object}	ErrorResponse
//	@Failure		504				{object}	ErrorResponse
//	@Router			/subscriptions/summary [get]
func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) {
	from, err := parseDateParam(r, "from")
	if err != nil {
		h.writeError(w, r, err)

		return
	}

	to, err := parseDateParam(r, "to")
	if err != nil {
		h.writeError(w, r, err)

		return
	}

	filter, err := parseFilter(r)
	if err != nil {
		h.writeError(w, r, err)

		return
	}

	total, err := h.service.Summary(r.Context(), from, to, filter)
	if err != nil {
		h.writeError(w, r, err)

		return
	}

	h.writeJSON(w, r, http.StatusOK, SummaryResponse{
		TotalPrice: total,
		From:       model.FormatDate(from),
		To:         model.FormatDate(to),
	})
}

// Health проверяет, что сервис жив и база отвечает.
//
// В swagger не выносится: ручка живёт вне /api/v1, а swagger склеил бы путь с
// basePath и запрашивал несуществующий /api/v1/healthz.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
	defer cancel()

	if err := h.pinger.Ping(ctx); err != nil {
		h.log.Error("база данных недоступна", slog.String("error", err.Error()))
		h.writeJSON(w, r, http.StatusServiceUnavailable, ErrorResponse{
			Code:    "unavailable",
			Message: "база данных недоступна",
		})

		return
	}

	h.writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// decodeRequest разбирает тело запроса.
//
// Неизвестные поля отвергаются из-за семантики PUT: он перезаписывает запись
// целиком, и опечатка в имени поля молча обнулила бы значение.
func decodeRequest(w http.ResponseWriter, r *http.Request) (SubscriptionRequest, error) {
	if err := checkContentType(r); err != nil {
		return SubscriptionRequest{}, err
	}

	var req SubscriptionRequest

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBodySize))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&req); err != nil {
		return SubscriptionRequest{}, decodeError(err)
	}

	// В теле должен быть ровно один JSON-объект: мусор после закрывающей
	// скобки иначе прошёл бы незамеченным.
	if decoder.More() {
		return SubscriptionRequest{}, fmt.Errorf("%w: в теле запроса больше одного JSON-значения", model.ErrValidation)
	}

	return req, nil
}

// clientError хранит два представления одной ошибки: публичную формулировку
// для клиента и исходную причину для лога.
type clientError struct {
	public error
	cause  error
}

func (e *clientError) Error() string { return e.public.Error() }

// Unwrap отдаёт обе ошибки, поэтому errors.Is находит и sentinel публичной
// формулировки, и исходную причину.
func (e *clientError) Unwrap() []error { return []error{e.public, e.cause} }

// withCause прячет исходную ошибку за публичной формулировкой.
func withCause(public, cause error) error {
	if cause == nil {
		return public
	}

	return &clientError{public: public, cause: cause}
}

// causeOf достаёт исходную причину, чтобы записать её в лог.
func causeOf(err error) error {
	var clientErr *clientError
	if errors.As(err, &clientErr) {
		return clientErr.cause
	}

	return nil
}

// decodeError переводит ошибку разбора JSON в сообщение для клиента.
//
// Текст стандартной библиотеки наружу не отдаётся: он содержит имена
// Go-структур и внутренних типов. Исходная ошибка сохраняется как причина и
// попадает в лог.
func decodeError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return withCause(fmt.Errorf("%w: не более %d МБ", errPayloadTooLarge, tooLarge.Limit>>20), err)
	}

	// Field — имя поля из запроса клиента, его показать можно.
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return withCause(fmt.Errorf("%w: поле %q имеет неверный тип", model.ErrValidation, typeErr.Field), err)
	}

	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return withCause(fmt.Errorf("%w: тело запроса не является корректным JSON (позиция %d)",
			model.ErrValidation, syntaxErr.Offset), err)
	}

	if errors.Is(err, io.EOF) {
		return withCause(fmt.Errorf("%w: тело запроса пустое", model.ErrValidation), err)
	}

	if field, ok := unknownField(err); ok {
		return withCause(fmt.Errorf("%w: неизвестное поле %q", model.ErrValidation, field), err)
	}

	return withCause(fmt.Errorf("%w: тело запроса не является корректным JSON", model.ErrValidation), err)
}

// unknownField достаёт имя поля из ошибки DisallowUnknownFields: своего типа
// для неё в стандартной библиотеке нет, только текст фиксированного формата.
func unknownField(err error) (string, bool) {
	const prefix = "json: unknown field "

	message := err.Error()
	if !strings.HasPrefix(message, prefix) {
		return "", false
	}

	return strings.Trim(strings.TrimPrefix(message, prefix), `"`), true
}

// checkContentType отвергает тело в чужом формате. Отсутствующий заголовок
// допускается: часть клиентов его не ставит.
func checkContentType(r *http.Request) error {
	value := r.Header.Get("Content-Type")
	if value == "" {
		return nil
	}

	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil || mediaType != "application/json" {
		return fmt.Errorf("%w: получено %q", errUnsupportedMediaType, value)
	}

	return nil
}

// subscriptionID читает идентификатор подписки из пути.
func subscriptionID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: идентификатор подписки должен быть корректным UUID", model.ErrValidation)
	}

	return id, nil
}

// parseDateParam читает обязательный параметр с датой в формате MM-YYYY.
func parseDateParam(r *http.Request, name string) (time.Time, error) {
	date, err := model.ParseDate(strings.TrimSpace(r.URL.Query().Get(name)))
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: параметр %s должен быть датой в формате MM-YYYY, например 07-2025", model.ErrValidation, name)
	}

	return date, nil
}

// parsePage читает limit и offset, подставляя значения по умолчанию.
// Слишком большой limit не считается ошибкой, а урезается до максимума.
func parsePage(r *http.Request) (model.Page, error) {
	limit, err := intParam(r, "limit", DefaultLimit)
	if err != nil {
		return model.Page{}, err
	}

	if limit < 1 {
		return model.Page{}, fmt.Errorf("%w: параметр limit должен быть больше нуля", model.ErrValidation)
	}

	if limit > MaxLimit {
		limit = MaxLimit
	}

	offset, err := intParam(r, "offset", 0)
	if err != nil {
		return model.Page{}, err
	}

	if offset < 0 {
		return model.Page{}, fmt.Errorf("%w: параметр offset не может быть отрицательным", model.ErrValidation)
	}

	return model.Page{Limit: limit, Offset: offset}, nil
}

// intParam читает целочисленный query-параметр или возвращает значение по умолчанию.
func intParam(r *http.Request, name string, fallback int) (int, error) {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return fallback, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%w: параметр %s должен быть целым числом", model.ErrValidation, name)
	}

	return parsed, nil
}

// parseFilter собирает необязательные фильтры из query-параметров.
func parseFilter(r *http.Request) (model.Filter, error) {
	var filter model.Filter

	if value := strings.TrimSpace(r.URL.Query().Get("user_id")); value != "" {
		userID, err := uuid.Parse(value)
		if err != nil {
			return filter, fmt.Errorf("%w: user_id должен быть корректным UUID", model.ErrValidation)
		}

		filter.UserID = &userID
	}

	if value := strings.TrimSpace(r.URL.Query().Get("service_name")); value != "" {
		filter.ServiceName = &value
	}

	return filter, nil
}

// writeJSON отправляет ответ в формате JSON.
func (h *Handler) writeJSON(w http.ResponseWriter, r *http.Request, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		h.logger(r).Error("не удалось сериализовать ответ", slog.String("error", err.Error()))
	}
}

// writeError переводит ошибку в HTTP-ответ: некорректные данные — 400,
// отсутствие записи — 404, остальное — 500 без деталей наружу.
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	log := h.logger(r)

	switch {
	// Клиент оборвал соединение: отвечать некому, и это не отказ сервиса —
	// иначе мониторинг по level=ERROR ловил бы каждый отменённый запрос.
	case errors.Is(err, context.Canceled):
		log.Info("запрос отменён клиентом")

	case errors.Is(err, context.DeadlineExceeded):
		log.Warn("превышено время обработки запроса", slog.String("error", err.Error()))
		h.writeJSON(w, r, http.StatusGatewayTimeout, ErrorResponse{
			Code:    "timeout",
			Message: errRequestTimeout.Error(),
		})

	case errors.Is(err, errPayloadTooLarge):
		log.Warn("тело запроса слишком большое", errorAttrs(err)...)
		h.writeJSON(w, r, http.StatusRequestEntityTooLarge, ErrorResponse{
			Code:    "payload_too_large",
			Message: err.Error(),
		})

	case errors.Is(err, errUnsupportedMediaType):
		log.Warn("неподдерживаемый тип содержимого", errorAttrs(err)...)
		h.writeJSON(w, r, http.StatusUnsupportedMediaType, ErrorResponse{
			Code:    "unsupported_media_type",
			Message: err.Error(),
		})

	case errors.Is(err, model.ErrValidation):
		log.Warn("некорректный запрос", errorAttrs(err)...)
		h.writeJSON(w, r, http.StatusBadRequest, ErrorResponse{
			Code:    "validation_error",
			Message: err.Error(),
		})

	case errors.Is(err, model.ErrNotFound):
		h.writeJSON(w, r, http.StatusNotFound, ErrorResponse{
			Code:    "not_found",
			Message: err.Error(),
		})

	default:
		// Наружу отдаём общую формулировку, подробности остаются в логе.
		log.Error("внутренняя ошибка", slog.String("error", err.Error()))
		h.writeJSON(w, r, http.StatusInternalServerError, ErrorResponse{
			Code:    "internal_error",
			Message: "внутренняя ошибка сервиса",
		})
	}
}

// errorAttrs собирает атрибуты для лога: публичную формулировку и, если она
// прячет исходную ошибку, саму причину. Клиент причины не видит.
func errorAttrs(err error) []any {
	attrs := []any{slog.String("error", err.Error())}

	if cause := causeOf(err); cause != nil {
		attrs = append(attrs, slog.String("cause", cause.Error()))
	}

	return attrs
}

// logger добавляет к логгеру идентификатор запроса, чтобы строку об ошибке
// можно было сопоставить со строкой доступа при разборе инцидента.
func (h *Handler) logger(r *http.Request) *slog.Logger {
	if r == nil {
		return h.log
	}

	return h.log.With(slog.String("request_id", middleware.GetReqID(r.Context())))
}
