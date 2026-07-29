package model_test

import (
	"errors"
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
		{name: "valid", input: "07-2025", want: time.Date(2025, time.July, 1, 0, 0, 0, 0, time.UTC)},
		{name: "january", input: "01-2025", want: time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)},
		{name: "month out of range", input: "13-2025", wantErr: true},
		{name: "full date", input: "23-07-2025", wantErr: true},
		{name: "wrong separator", input: "07/2025", wantErr: true},
		{name: "empty", input: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := model.ParseDate(tt.input)

			if tt.wantErr {
				if !errors.Is(err, model.ErrValidation) {
					t.Fatalf("ParseDate(%q) error = %v, want ErrValidation", tt.input, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("ParseDate(%q) returned unexpected error: %v", tt.input, err)
			}

			if !got.Equal(tt.want) {
				t.Errorf("ParseDate(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatDate(t *testing.T) {
	got := model.FormatDate(time.Date(2025, time.July, 1, 0, 0, 0, 0, time.UTC))

	if got != "07-2025" {
		t.Errorf("FormatDate() = %q, want %q", got, "07-2025")
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
		{name: "valid subscription", mutate: func(*model.Subscription) {}},
		{name: "zero price is allowed", mutate: func(s *model.Subscription) { s.Price = 0 }},
		{name: "empty service name", mutate: func(s *model.Subscription) { s.ServiceName = "" }, wantErr: true},
		{name: "negative price", mutate: func(s *model.Subscription) { s.Price = -1 }, wantErr: true},
		{name: "empty user id", mutate: func(s *model.Subscription) { s.UserID = uuid.Nil }, wantErr: true},
		{name: "empty start date", mutate: func(s *model.Subscription) { s.StartDate = time.Time{} }, wantErr: true},
		{name: "end date before start date", mutate: func(s *model.Subscription) { s.EndDate = &earlier }, wantErr: true},
		{name: "end date equals start date", mutate: func(s *model.Subscription) { s.EndDate = &startDate }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := valid()
			tt.mutate(&sub)

			err := sub.Validate()

			if tt.wantErr && !errors.Is(err, model.ErrValidation) {
				t.Fatalf("Validate() error = %v, want ErrValidation", err)
			}

			if !tt.wantErr && err != nil {
				t.Fatalf("Validate() returned unexpected error: %v", err)
			}
		})
	}
}
