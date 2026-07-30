package model_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"sobes_stackbridge_go/internal/model"
)

func TestParseDate(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Time
		wantErr bool
	}{
		{name: "корректная дата", input: "07-2025", want: time.Date(2025, time.July, 1, 0, 0, 0, 0, time.UTC)},
		{name: "январь", input: "01-2025", want: time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)},
		{name: "месяц вне диапазона", input: "13-2025", wantErr: true},
		{name: "дата с днём", input: "23-07-2025", wantErr: true},
		{name: "чужой разделитель", input: "07/2025", wantErr: true},
		{name: "пустая строка", input: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := model.ParseDate(tt.input)

			if tt.wantErr {
				if !errors.Is(err, model.ErrValidation) {
					t.Fatalf("ParseDate(%q) вернул ошибку %v, ожидалась ErrValidation", tt.input, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("ParseDate(%q) вернул неожиданную ошибку: %v", tt.input, err)
			}

			if !got.Equal(tt.want) {
				t.Errorf("ParseDate(%q) = %v, ожидалось %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatDate(t *testing.T) {
	got := model.FormatDate(time.Date(2025, time.July, 1, 0, 0, 0, 0, time.UTC))

	if got != "07-2025" {
		t.Errorf("FormatDate() = %q, ожидалось %q", got, "07-2025")
	}
}

func TestValidate(t *testing.T) {
	startDate := time.Date(2025, time.July, 1, 0, 0, 0, 0, time.UTC)
	earlier := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)

	valid := func() model.Subscription {
		return model.Subscription{
			ServiceName: "Yandex Plus",
			Price:       400,
			UserID:      uuid.MustParse("60601fee-2bf1-4721-ae6f-7636e79a0cba"),
			StartDate:   startDate,
		}
	}

	tests := []struct {
		name    string
		mutate  func(*model.Subscription)
		wantErr bool
	}{
		{name: "корректная подписка", mutate: func(*model.Subscription) {}},
		{name: "нулевая стоимость допустима", mutate: func(s *model.Subscription) { s.Price = 0 }},
		{name: "пустое название сервиса", mutate: func(s *model.Subscription) { s.ServiceName = "" }, wantErr: true},
		{name: "отрицательная стоимость", mutate: func(s *model.Subscription) { s.Price = -1 }, wantErr: true},
		{name: "не указан пользователь", mutate: func(s *model.Subscription) { s.UserID = uuid.Nil }, wantErr: true},
		{name: "не указана дата начала", mutate: func(s *model.Subscription) { s.StartDate = time.Time{} }, wantErr: true},
		{name: "дата окончания раньше начала", mutate: func(s *model.Subscription) { s.EndDate = &earlier }, wantErr: true},
		{name: "дата окончания равна началу", mutate: func(s *model.Subscription) { s.EndDate = &startDate }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := valid()
			tt.mutate(&sub)

			err := sub.Validate()

			if tt.wantErr && !errors.Is(err, model.ErrValidation) {
				t.Fatalf("Validate() вернул ошибку %v, ожидалась ErrValidation", err)
			}

			if !tt.wantErr && err != nil {
				t.Fatalf("Validate() вернул неожиданную ошибку: %v", err)
			}
		})
	}
}

// TestValidateCollectsAllViolations закрепляет, что проверка не обрывается на
// первом нарушении.
func TestValidateCollectsAllViolations(t *testing.T) {
	endDate := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)

	sub := model.Subscription{
		ServiceName: "",
		Price:       -1,
		UserID:      uuid.Nil,
		StartDate:   time.Date(2025, time.July, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     &endDate,
	}

	err := sub.Validate()

	if !errors.Is(err, model.ErrValidation) {
		t.Fatalf("Validate() вернул ошибку %v, ожидалась ErrValidation", err)
	}

	violations := model.Violations(err)
	if len(violations) != 4 {
		t.Fatalf("нарушений = %d (%q), ожидалось 4", len(violations), violations)
	}

	// В сообщении должны быть перечислены все нарушения, а не первое.
	for _, want := range violations {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в сообщении нет нарушения %q: %s", want, err.Error())
		}
	}
}

// TestValidateReportsExclusiveViolationOnce: пустое название не может быть
// одновременно слишком длинным, поэтому нарушение по полю ровно одно.
func TestValidateReportsExclusiveViolationOnce(t *testing.T) {
	sub := model.Subscription{
		ServiceName: "",
		Price:       400,
		UserID:      uuid.MustParse("60601fee-2bf1-4721-ae6f-7636e79a0cba"),
		StartDate:   time.Date(2025, time.July, 1, 0, 0, 0, 0, time.UTC),
	}

	violations := model.Violations(sub.Validate())
	if len(violations) != 1 {
		t.Errorf("нарушений = %d (%q), ожидалось 1", len(violations), violations)
	}
}
