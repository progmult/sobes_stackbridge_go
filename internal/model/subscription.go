// Package model содержит доменную модель подписки.
package model

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// DateLayout — формат даты в API: месяц и год, например "07-2025".
const DateLayout = "01-2006"

// Границы значений повторяют ограничения колонок: иначе некорректный ввод
// доходил бы до Postgres и возвращался клиенту как 500.
const (
	// MaxServiceNameLength соответствует VARCHAR(255).
	MaxServiceNameLength = 255
	// MaxPrice соответствует INTEGER: Go int шире, чем колонка в БД.
	MaxPrice = math.MaxInt32
)

var (
	// ErrNotFound — записи о подписке нет в базе.
	ErrNotFound = errors.New("подписка не найдена")
	// ErrValidation — некорректные входные данные.
	ErrValidation = errors.New("некорректный запрос")
	// ErrConflict — запрос корректен, но противоречит уже сохранённым данным:
	// у пользователя есть подписка на тот же сервис за пересекающийся период.
	ErrConflict = errors.New("подписка на этот сервис уже есть за пересекающийся период")
)

// Subscription — запись об онлайн-подписке пользователя.
// Даты хранятся как первое число месяца: дни в задаче не учитываются.
type Subscription struct {
	ID          uuid.UUID
	ServiceName string
	Price       int
	UserID      uuid.UUID
	StartDate   time.Time
	EndDate     *time.Time
}

// Filter — необязательные фильтры выборки. nil означает «не фильтровать».
type Filter struct {
	UserID      *uuid.UUID
	ServiceName *string
}

// Page — постраничная навигация. Границы размера страницы задаёт
// транспортный слой, сюда значения приходят уже проверенными.
type Page struct {
	Limit  int
	Offset int
}

// ParseDate разбирает дату вида "07-2025" в первое число месяца.
func ParseDate(value string) (time.Time, error) {
	date, err := time.Parse(DateLayout, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: значение %q не является датой в формате MM-YYYY", ErrValidation, value)
	}

	return date, nil
}

// FormatDate приводит дату к формату "07-2025".
func FormatDate(date time.Time) string {
	return date.Format(DateLayout)
}

// Имена полей записи для сообщений об ошибках. Одни и те же имена носят поля
// JSON и колонки таблицы, поэтому отдельного словаря для перевода между слоями
// не нужно.
const (
	FieldServiceName = "service_name"
	FieldPrice       = "price"
	FieldUserID      = "user_id"
	FieldStartDate   = "start_date"
	FieldEndDate     = "end_date"
)

// Violation — одно нарушение с указанием поля, к которому оно относится.
// Сообщение сформулировано относительно поля («не может быть пустым»), чтобы
// имя не дублировалось в тексте.
type Violation struct {
	Field   string
	Message string
}

func (v Violation) String() string { return v.Field + ": " + v.Message }

// ValidationError перечисляет все нарушения сразу: клиенту удобнее увидеть
// полный список, чем чинить поля по одному запросу.
type ValidationError struct {
	Violations []Violation
}

// NewValidationError собирает ошибку из перечня нарушений. Пустой перечень
// означает, что нарушений нет, и возвращается nil.
func NewValidationError(violations ...Violation) error {
	if len(violations) == 0 {
		return nil
	}

	return &ValidationError{Violations: violations}
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Violations))
	for _, violation := range e.Violations {
		parts = append(parts, violation.String())
	}

	return ErrValidation.Error() + ": " + strings.Join(parts, "; ")
}

// Unwrap возвращает sentinel, поэтому errors.Is(err, ErrValidation) работает
// так же, как для одиночных ошибок валидации.
func (e *ValidationError) Unwrap() error { return ErrValidation }

// Violations достаёт перечень нарушений, если ошибка его несёт.
func Violations(err error) []Violation {
	var validationErr *ValidationError
	if errors.As(err, &validationErr) {
		return validationErr.Violations
	}

	return nil
}

// Validate проверяет запись перед сохранением в базу и возвращает сразу все
// нарушения, а не первое встреченное.
// Проверка рассчитана и на частично собранную запись: транспорт вызывает её
// после разбора тела, когда часть полей могла не разобраться. Про такое поле
// здесь скажут «не указано», и это то же нарушение, о котором транспорт уже
// сообщил точнее, — лишнее он отсеет сам.
func (s *Subscription) Validate() error {
	var violations []Violation

	// Взаимоисключающие проверки одного поля остаются switch: пустое название
	// не может быть одновременно слишком длинным.
	switch {
	case s.ServiceName == "":
		violations = append(violations, Violation{FieldServiceName, "не может быть пустым"})
	case utf8.RuneCountInString(s.ServiceName) > MaxServiceNameLength:
		violations = append(violations, Violation{
			FieldServiceName,
			fmt.Sprintf("длиннее %d символов", MaxServiceNameLength),
		})
	}

	switch {
	case s.Price < 0:
		violations = append(violations, Violation{FieldPrice, "не может быть отрицательной"})
	case s.Price > MaxPrice:
		violations = append(violations, Violation{FieldPrice, fmt.Sprintf("не может превышать %d", MaxPrice)})
	}

	if s.UserID == uuid.Nil {
		violations = append(violations, Violation{FieldUserID, "не указан"})
	}

	if s.StartDate.IsZero() {
		violations = append(violations, Violation{FieldStartDate, "не указана"})
	}

	if s.EndDate != nil && s.EndDate.Before(s.StartDate) {
		violations = append(violations, Violation{FieldEndDate, "не может быть раньше даты начала"})
	}

	return NewValidationError(violations...)
}
