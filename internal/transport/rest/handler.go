// Package rest содержит HTTP-слой сервиса: маршруты, обработчики и DTO.
package rest

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"sobes_stackbridge_go/internal/model"
	"sobes_stackbridge_go/internal/service"
)

// Handler обслуживает HTTP-запросы к подпискам.
type Handler struct {
	service *service.Service
	log     *slog.Logger
}

// NewHandler создаёт обработчик подписок.
func NewHandler(svc *service.Service, log *slog.Logger) *Handler {
	return &Handler{service: svc, log: log}
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
//	@Failure		500		{object}	ErrorResponse
//	@Router			/subscriptions [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var req SubscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, fmt.Errorf("%w: тело запроса должно быть корректным JSON-объектом", model.ErrValidation))

		return
	}

	sub, err := req.toModel(uuid.Nil)
	if err != nil {
		h.writeError(w, err)

		return
	}

	created, err := h.service.Create(r.Context(), sub)
	if err != nil {
		h.writeError(w, err)

		return
	}

	// Location на 201 — требование REST: клиент узнаёт адрес созданного ресурса.
	w.Header().Set("Location", "/api/v1/subscriptions/"+created.ID.String())
	h.writeJSON(w, http.StatusCreated, newSubscriptionResponse(created))
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
//	@Router			/subscriptions/{id} [get]
func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		h.writeError(w, fmt.Errorf("%w: идентификатор подписки должен быть корректным UUID", model.ErrValidation))

		return
	}

	sub, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		h.writeError(w, err)

		return
	}

	h.writeJSON(w, http.StatusOK, newSubscriptionResponse(sub))
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
//	@Failure		500		{object}	ErrorResponse
//	@Router			/subscriptions/{id} [put]
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		h.writeError(w, fmt.Errorf("%w: идентификатор подписки должен быть корректным UUID", model.ErrValidation))

		return
	}

	var req SubscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, fmt.Errorf("%w: тело запроса должно быть корректным JSON-объектом", model.ErrValidation))

		return
	}

	sub, err := req.toModel(id)
	if err != nil {
		h.writeError(w, err)

		return
	}

	updated, err := h.service.Update(r.Context(), sub)
	if err != nil {
		h.writeError(w, err)

		return
	}

	h.writeJSON(w, http.StatusOK, newSubscriptionResponse(updated))
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
//	@Router			/subscriptions/{id} [delete]
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		h.writeError(w, fmt.Errorf("%w: идентификатор подписки должен быть корректным UUID", model.ErrValidation))

		return
	}

	if err := h.service.Delete(r.Context(), id); err != nil {
		h.writeError(w, err)

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
//	@Router			/subscriptions [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	filter, err := parseFilter(r)
	if err != nil {
		h.writeError(w, err)

		return
	}

	page, err := parsePage(r)
	if err != nil {
		h.writeError(w, err)

		return
	}

	subscriptions, total, err := h.service.List(r.Context(), filter, page)
	if err != nil {
		h.writeError(w, err)

		return
	}

	items := make([]SubscriptionResponse, 0, len(subscriptions))
	for i := range subscriptions {
		items = append(items, newSubscriptionResponse(&subscriptions[i]))
	}

	h.writeJSON(w, http.StatusOK, ListResponse{
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
//	@Router			/subscriptions/summary [get]
func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) {
	from, err := parseDateParam(r, "from")
	if err != nil {
		h.writeError(w, err)

		return
	}

	to, err := parseDateParam(r, "to")
	if err != nil {
		h.writeError(w, err)

		return
	}

	filter, err := parseFilter(r)
	if err != nil {
		h.writeError(w, err)

		return
	}

	total, err := h.service.Summary(r.Context(), from, to, filter)
	if err != nil {
		h.writeError(w, err)

		return
	}

	h.writeJSON(w, http.StatusOK, SummaryResponse{
		TotalPrice: total,
		From:       model.FormatDate(from),
		To:         model.FormatDate(to),
	})
}

// Health godoc
//
//	@Summary	Проверка доступности сервиса
//	@Tags		health
//	@Produce	json
//	@Success	200	{object}	map[string]string
//	@Router		/healthz [get]
func (h *Handler) Health(w http.ResponseWriter, _ *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
	limit, err := intParam(r, "limit", model.DefaultLimit)
	if err != nil {
		return model.Page{}, err
	}

	if limit < 1 {
		return model.Page{}, fmt.Errorf("%w: параметр limit должен быть больше нуля", model.ErrValidation)
	}

	if limit > model.MaxLimit {
		limit = model.MaxLimit
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
func (h *Handler) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		h.log.Error("не удалось сериализовать ответ", slog.String("error", err.Error()))
	}
}

// writeError переводит ошибку в HTTP-ответ: некорректные данные — 400,
// отсутствие записи — 404, остальное — 500 без деталей наружу.
func (h *Handler) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, model.ErrValidation):
		h.log.Warn("некорректный запрос", slog.String("error", err.Error()))
		h.writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Code:    "validation_error",
			Message: err.Error(),
		})

	case errors.Is(err, model.ErrNotFound):
		h.writeJSON(w, http.StatusNotFound, ErrorResponse{
			Code:    "not_found",
			Message: err.Error(),
		})

	default:
		// Наружу отдаём общую формулировку, подробности остаются в логе.
		h.log.Error("внутренняя ошибка", slog.String("error", err.Error()))
		h.writeJSON(w, http.StatusInternalServerError, ErrorResponse{
			Code:    "internal_error",
			Message: "внутренняя ошибка сервиса",
		})
	}
}
