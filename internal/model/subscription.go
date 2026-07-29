// Package model содержит доменную модель подписки.
package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DateLayout — формат даты в API: месяц и год, например "07-2025".
const DateLayout = "01-2006"

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

// Validate проверяет запись перед сохранением в базу.
func (s *Subscription) Validate() error {
	switch {
	case s.ServiceName == "":
		return fmt.Errorf("%w: название сервиса не может быть пустым", ErrValidation)
	case s.Price < 0:
		return fmt.Errorf("%w: стоимость подписки не может быть отрицательной", ErrValidation)
	case s.UserID == uuid.Nil:
		return fmt.Errorf("%w: не указан идентификатор пользователя", ErrValidation)
	case s.StartDate.IsZero():
		return fmt.Errorf("%w: не указана дата начала подписки", ErrValidation)
	case s.EndDate != nil && s.EndDate.Before(s.StartDate):
		return fmt.Errorf("%w: дата окончания не может быть раньше даты начала", ErrValidation)
	}

	return nil
}
