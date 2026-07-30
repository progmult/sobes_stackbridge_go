package rest

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"sobes_stackbridge_go/internal/model"
)

// SubscriptionRequest — тело запроса на создание или обновление подписки.
type SubscriptionRequest struct {
	// Название сервиса, предоставляющего подписку.
	ServiceName string `json:"service_name" example:"Yandex Plus"`
	// Стоимость месячной подписки в рублях, целое число.
	Price *int `json:"price" example:"400"`
	// ID пользователя в формате UUID.
	UserID string `json:"user_id" example:"60601fee-2bf1-4721-ae6f-7636e79a0cba"`
	// Месяц и год начала подписки, формат MM-YYYY.
	StartDate string `json:"start_date" example:"07-2025"`
	// Месяц и год окончания подписки, формат MM-YYYY. Поле необязательное.
	EndDate string `json:"end_date,omitempty" example:"12-2025"`
}

// toModel переводит тело запроса в доменную модель, проверяя формат полей.
// Разбор не прекращается на первой ошибке: клиент получает перечень всех
// полей, которые надо починить.
//
// Инварианты записи проверяет model.Validate уже после разбора — на значениях,
// которые не удалось разобрать, проверять нечего.
func (r SubscriptionRequest) toModel(id uuid.UUID) (*model.Subscription, error) {
	var violations []string

	price := 0
	if r.Price == nil {
		violations = append(violations, "не указана стоимость подписки")
	} else {
		price = *r.Price
	}

	userID, err := uuid.Parse(strings.TrimSpace(r.UserID))
	if err != nil {
		violations = append(violations, "user_id должен быть корректным UUID")
	}

	startDate, err := model.ParseDate(strings.TrimSpace(r.StartDate))
	if err != nil {
		violations = append(violations, "дата начала должна быть в формате MM-YYYY, например 07-2025")
	}

	var endDate *time.Time

	if strings.TrimSpace(r.EndDate) != "" {
		parsed, err := model.ParseDate(strings.TrimSpace(r.EndDate))
		if err != nil {
			violations = append(violations, "дата окончания должна быть в формате MM-YYYY, например 12-2025")
		} else {
			endDate = &parsed
		}
	}

	if err := model.NewValidationError(violations...); err != nil {
		return nil, err
	}

	return &model.Subscription{
		ID:          id,
		ServiceName: r.ServiceName,
		Price:       price,
		UserID:      userID,
		StartDate:   startDate,
		EndDate:     endDate,
	}, nil
}

// SubscriptionResponse — представление подписки в ответах API.
type SubscriptionResponse struct {
	ID          string `json:"id"           example:"7c6f1e2a-6b4e-4c67-9f3f-2f2a1d6b8c11"`
	ServiceName string `json:"service_name" example:"Yandex Plus"`
	Price       int    `json:"price"        example:"400"`
	UserID      string `json:"user_id"      example:"60601fee-2bf1-4721-ae6f-7636e79a0cba"`
	StartDate   string `json:"start_date"   example:"07-2025"`
	EndDate     string `json:"end_date,omitempty" example:"12-2025"`
}

func newSubscriptionResponse(sub *model.Subscription) SubscriptionResponse {
	resp := SubscriptionResponse{
		ID:          sub.ID.String(),
		ServiceName: sub.ServiceName,
		Price:       sub.Price,
		UserID:      sub.UserID.String(),
		StartDate:   model.FormatDate(sub.StartDate),
	}

	if sub.EndDate != nil {
		resp.EndDate = model.FormatDate(*sub.EndDate)
	}

	return resp
}

// ListResponse — страница списка подписок. Ответ обёрнут в объект, а не
// отдан голым массивом, чтобы рядом с данными поместились total/limit/offset.
type ListResponse struct {
	// Записи текущей страницы. Пустая страница — это [], а не null.
	Items []SubscriptionResponse `json:"items"`
	// Сколько всего записей подходит под фильтр, без учёта постраничности.
	Total int `json:"total" example:"42"`
	// Размер страницы, применённый сервисом.
	Limit int `json:"limit" example:"50"`
	// Смещение от начала выборки.
	Offset int `json:"offset" example:"0"`
}

// SummaryResponse — суммарная стоимость подписок за период.
type SummaryResponse struct {
	// Суммарная стоимость в рублях за весь период.
	TotalPrice int64 `json:"total_price" example:"4800"`
	// Начало периода включительно, MM-YYYY.
	From string `json:"from" example:"01-2025"`
	// Конец периода включительно, MM-YYYY.
	To string `json:"to" example:"12-2025"`
}

// ErrorResponse — единый формат ошибки API: машиночитаемый код плюс
// человекочитаемое сообщение.
type ErrorResponse struct {
	// Код ошибки для обработки на клиенте.
	Code string `json:"code" example:"validation_error"`
	// Пояснение для человека на русском, без внутренних деталей реализации.
	Message string `json:"message" example:"некорректный запрос: стоимость подписки не может быть отрицательной"`
	// Перечень нарушений, если их несколько. Заполняется при проверке тела
	// запроса, чтобы клиент увидел все проблемные поля разом.
	Details []string `json:"details,omitempty" example:"название сервиса не может быть пустым,стоимость подписки не может быть отрицательной"`
}
