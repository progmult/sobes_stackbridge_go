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

// ValidationError перечисляет все нарушения сразу: клиенту удобнее увидеть
// полный список, чем чинить поля по одному запросу.
type ValidationError struct {
	Violations []string
}

// NewValidationError собирает ошибку из перечня нарушений. Пустой перечень
// означает, что нарушений нет, и возвращается nil.
func NewValidationError(violations ...string) error {
	if len(violations) == 0 {
		return nil
	}

	return &ValidationError{Violations: violations}
}

func (e *ValidationError) Error() string {
	return ErrValidation.Error() + ": " + strings.Join(e.Violations, "; ")
}

// Unwrap возвращает sentinel, поэтому errors.Is(err, ErrValidation) работает
// так же, как для одиночных ошибок валидации.
func (e *ValidationError) Unwrap() error { return ErrValidation }

// Violations достаёт перечень нарушений, если ошибка его несёт.
func Violations(err error) []string {
	var validationErr *ValidationError
	if errors.As(err, &validationErr) {
		return validationErr.Violations
	}

	return nil
}

// Validate проверяет запись перед сохранением в базу и возвращает сразу все
// нарушения, а не первое встреченное.
func (s *Subscription) Validate() error {
	var violations []string

	// Взаимоисключающие проверки одного поля остаются switch: пустое название
	// не может быть одновременно слишком длинным.
	switch {
	case s.ServiceName == "":
		violations = append(violations, "название сервиса не может быть пустым")
	case utf8.RuneCountInString(s.ServiceName) > MaxServiceNameLength:
		violations = append(violations, fmt.Sprintf("название сервиса длиннее %d символов", MaxServiceNameLength))
	}

	switch {
	case s.Price < 0:
		violations = append(violations, "стоимость подписки не может быть отрицательной")
	case s.Price > MaxPrice:
		violations = append(violations, fmt.Sprintf("стоимость подписки не может превышать %d", MaxPrice))
	}

	if s.UserID == uuid.Nil {
		violations = append(violations, "не указан идентификатор пользователя")
	}

	if s.StartDate.IsZero() {
		violations = append(violations, "не указана дата начала подписки")
	}

	if s.EndDate != nil && s.EndDate.Before(s.StartDate) {
		violations = append(violations, "дата окончания не может быть раньше даты начала")
	}

	return NewValidationError(violations...)
}
