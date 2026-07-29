package service_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"sobes_stackbridge_go/internal/model"
	"sobes_stackbridge_go/internal/service"
)

// repoStub — заглушка хранилища: запоминает то, что ей передали.
type repoStub struct {
	created *model.Subscription
	page    service.Page
	from    time.Time
	to      time.Time
	sum     int64
}

func (r *repoStub) Create(_ context.Context, sub *model.Subscription) (*model.Subscription, error) {
	sub.ID = uuid.New()
	r.created = sub

	return sub, nil
}

func (r *repoStub) GetByID(context.Context, uuid.UUID) (*model.Subscription, error) {
	return nil, model.ErrNotFound
}

func (r *repoStub) Update(_ context.Context, sub *model.Subscription) (*model.Subscription, error) {
	return sub, nil
}

func (r *repoStub) Delete(context.Context, uuid.UUID) error {
	return nil
}

func (r *repoStub) List(_ context.Context, _ service.Filter, page service.Page) ([]model.Subscription, int, error) {
	r.page = page

	return nil, 0, nil
}

func (r *repoStub) SumForPeriod(_ context.Context, from, to time.Time, _ service.Filter) (int64, error) {
	r.from, r.to = from, to

	return r.sum, nil
}

func newService(repo service.Repository) *service.Service {
	return service.New(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestCreateRejectsInvalidSubscription(t *testing.T) {
	sub := &model.Subscription{
		ServiceName: "Yandex Plus",
		Price:       -1,
		UserID:      uuid.MustParse("60601fee-2bf1-4721-ae6f-7636e79a0cba"),
		StartDate:   time.Date(2025, time.July, 1, 0, 0, 0, 0, time.UTC),
	}

	_, err := newService(&repoStub{}).Create(context.Background(), sub)

	if !errors.Is(err, model.ErrValidation) {
		t.Fatalf("Create() error = %v, want ErrValidation", err)
	}
}

func TestCreateTrimsServiceName(t *testing.T) {
	repo := &repoStub{}

	sub := &model.Subscription{
		ServiceName: "  Yandex Plus  ",
		Price:       400,
		UserID:      uuid.MustParse("60601fee-2bf1-4721-ae6f-7636e79a0cba"),
		StartDate:   time.Date(2025, time.July, 1, 0, 0, 0, 0, time.UTC),
	}

	if _, err := newService(repo).Create(context.Background(), sub); err != nil {
		t.Fatalf("Create() returned unexpected error: %v", err)
	}

	if repo.created.ServiceName != "Yandex Plus" {
		t.Errorf("ServiceName = %q, want %q", repo.created.ServiceName, "Yandex Plus")
	}
}

func TestSummaryRejectsInvertedPeriod(t *testing.T) {
	from := time.Date(2025, time.December, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)

	_, err := newService(&repoStub{}).Summary(context.Background(), from, to, service.Filter{})

	if !errors.Is(err, model.ErrValidation) {
		t.Fatalf("Summary() error = %v, want ErrValidation", err)
	}
}

func TestSummaryReturnsRepositoryTotal(t *testing.T) {
	from := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, time.December, 1, 0, 0, 0, 0, time.UTC)

	repo := &repoStub{sum: 4800}

	total, err := newService(repo).Summary(context.Background(), from, to, service.Filter{})
	if err != nil {
		t.Fatalf("Summary() returned unexpected error: %v", err)
	}

	if total != 4800 {
		t.Errorf("Summary() = %d, want %d", total, 4800)
	}

	if !repo.from.Equal(from) || !repo.to.Equal(to) {
		t.Errorf("repository got period %v..%v, want %v..%v", repo.from, repo.to, from, to)
	}
}
