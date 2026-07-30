package rest

import (
	"cmp"
	"slices"
	"strings"

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

// fieldOrder задаёт порядок полей в перечне нарушений — тот же, что в теле
// запроса. Без него порядок зависел бы от того, на каком этапе поле споткнулось.
// Поля, не попавшие в список, не теряются, а уходят в конец перечня.
var fieldOrder = []string{
	model.FieldServiceName,
	model.FieldPrice,
	model.FieldUserID,
	model.FieldStartDate,
	model.FieldEndDate,
}

// toModel переводит тело запроса в доменную модель, проверяя поля. Проверка не
// прекращается на первой ошибке: клиент получает перечень всех полей, которые
// надо починить, за один запрос.
//
// Этапов два — разбор строк здесь и инварианты в model.Validate, — но ранний
// возврат между ними не делается: запись собирается из того, что разобралось,
// и проверяется целиком. Иначе пустое название молчало бы, пока клиент чинит
// формат даты в соседнем поле.
func (r SubscriptionRequest) toModel(id uuid.UUID) (*model.Subscription, error) {
	// Пробелы срезаются до проверки, иначе название из одних пробелов прошло бы
	// как непустое.
	sub := &model.Subscription{ID: id, ServiceName: strings.TrimSpace(r.ServiceName)}

	var parseViolations []model.Violation

	if r.Price == nil {
		parseViolations = append(parseViolations, model.Violation{Field: model.FieldPrice, Message: "не указана"})
	} else {
		sub.Price = *r.Price
	}

	if userID, err := uuid.Parse(strings.TrimSpace(r.UserID)); err != nil {
		parseViolations = append(parseViolations, model.Violation{
			Field:   model.FieldUserID,
			Message: "должен быть корректным UUID",
		})
	} else {
		sub.UserID = userID
	}

	if startDate, err := model.ParseDate(strings.TrimSpace(r.StartDate)); err != nil {
		parseViolations = append(parseViolations, model.Violation{
			Field:   model.FieldStartDate,
			Message: "должна быть в формате MM-YYYY, например 07-2025",
		})
	} else {
		sub.StartDate = startDate
	}

	if strings.TrimSpace(r.EndDate) != "" {
		if endDate, err := model.ParseDate(strings.TrimSpace(r.EndDate)); err != nil {
			parseViolations = append(parseViolations, model.Violation{
				Field:   model.FieldEndDate,
				Message: "должна быть в формате MM-YYYY, например 12-2025",
			})
		} else {
			sub.EndDate = &endDate
		}
	}

	if err := model.NewValidationError(mergeViolations(parseViolations, model.Violations(sub.Validate()))...); err != nil {
		return nil, err
	}

	return sub, nil
}

// mergeViolations сводит нарушения обоих этапов проверки: по каждому полю
// остаётся одно, из первой группы, где оно встретилось.
//
// Порядок групп поэтому важен: у неразобранного поля Validate видит нулевое
// значение и скажет «не указано», хотя точная причина — формат. Побеждает
// сообщение о разборе.
func mergeViolations(groups ...[]model.Violation) []model.Violation {
	reported := make(map[string]bool, len(fieldOrder))
	merged := make([]model.Violation, 0, len(fieldOrder))

	for _, group := range groups {
		for _, violation := range group {
			if reported[violation.Field] {
				continue
			}

			reported[violation.Field] = true

			merged = append(merged, violation)
		}
	}

	slices.SortStableFunc(merged, func(a, b model.Violation) int {
		return cmp.Compare(fieldRank(a.Field), fieldRank(b.Field))
	})

	return merged
}

// fieldRank — позиция поля в fieldOrder. Поле, которого там нет, уходит в конец,
// а не выбрасывается: fieldOrder задаёт порядок, но не решает, о чём сообщать.
// Иначе забытая в нём строка молча выключила бы проверку нового поля — и запрос
// с единственным таким нарушением был бы принят как корректный.
func fieldRank(field string) int {
	if rank := slices.Index(fieldOrder, field); rank >= 0 {
		return rank
	}

	return len(fieldOrder)
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
	Message string `json:"message" example:"некорректный запрос: price: не может быть отрицательной"`
	// Перечень нарушений с указанием полей. Заполняется при проверке тела
	// запроса, чтобы клиент увидел все проблемные поля разом и мог подсветить
	// каждое у себя в форме.
	Details []ViolationResponse `json:"details,omitempty"`
}

// ViolationResponse — одно нарушение: имя поля и что с ним не так.
type ViolationResponse struct {
	// Имя поля из тела запроса.
	Field string `json:"field" example:"price"`
	// Что нарушено, формулировка относительно поля.
	Message string `json:"message" example:"не может быть отрицательной"`
}

// newViolationResponses переводит нарушения из домена в представление ответа.
func newViolationResponses(violations []model.Violation) []ViolationResponse {
	if len(violations) == 0 {
		return nil
	}

	details := make([]ViolationResponse, 0, len(violations))
	for _, violation := range violations {
		details = append(details, ViolationResponse{Field: violation.Field, Message: violation.Message})
	}

	return details
}
