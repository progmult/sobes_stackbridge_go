// Package service содержит бизнес-логику работы с подписками.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"sobes_stackbridge_go/internal/model"
)

// Ограничения размера страницы списка подписок.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// Filter — необязательные фильтры выборки. nil означает «не фильтровать».
type Filter struct {
	UserID      *uuid.UUID
	ServiceName *string
}

// Page — постраничная навигация. Значения приходят уже проверенными
// из транспортного слоя.
type Page struct {
	Limit  int
	Offset int
}

// Repository — контракт хранилища подписок.
type Repository interface {
	Create(ctx context.Context, sub *model.Subscription) (*model.Subscription, error)
	GetByID(ctx context.Context, id uuid.UUID) (*model.Subscription, error)
	Update(ctx context.Context, sub *model.Subscription) (*model.Subscription, error)
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, filter Filter, page Page) ([]model.Subscription, int, error)
	SumForPeriod(ctx context.Context, from, to time.Time, filter Filter) (int64, error)
}

// Service реализует сценарии работы с подписками.
type Service struct {
	repo Repository
	log  *slog.Logger
}

// New создаёт сервис подписок.
func New(repo Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// Create создаёт новую запись о подписке.
func (s *Service) Create(ctx context.Context, sub *model.Subscription) (*model.Subscription, error) {
	sub.ServiceName = strings.TrimSpace(sub.ServiceName)

	if err := sub.Validate(); err != nil {
		return nil, err
	}

	created, err := s.repo.Create(ctx, sub)
	if err != nil {
		return nil, err
	}

	s.log.Info("подписка создана",
		slog.String("id", created.ID.String()),
		slog.String("user_id", created.UserID.String()),
		slog.String("service_name", created.ServiceName),
		slog.Int("price", created.Price),
	)

	return created, nil
}

// GetByID возвращает подписку по идентификатору.
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (*model.Subscription, error) {
	return s.repo.GetByID(ctx, id)
}

// Update перезаписывает запись о подписке.
func (s *Service) Update(ctx context.Context, sub *model.Subscription) (*model.Subscription, error) {
	sub.ServiceName = strings.TrimSpace(sub.ServiceName)

	if err := sub.Validate(); err != nil {
		return nil, err
	}

	updated, err := s.repo.Update(ctx, sub)
	if err != nil {
		return nil, err
	}

	s.log.Info("подписка обновлена", slog.String("id", updated.ID.String()))

	return updated, nil
}

// Delete удаляет подписку.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}

	s.log.Info("подписка удалена", slog.String("id", id.String()))

	return nil
}

// List возвращает страницу подписок с учётом фильтров и общее количество
// записей, подходящих под фильтр.
func (s *Service) List(ctx context.Context, filter Filter, page Page) ([]model.Subscription, int, error) {
	return s.repo.List(ctx, filter, page)
}

// Summary считает суммарную стоимость подписок за период [from, to] включительно.
func (s *Service) Summary(ctx context.Context, from, to time.Time, filter Filter) (int64, error) {
	if to.Before(from) {
		return 0, fmt.Errorf("%w: конец периода не может быть раньше его начала", model.ErrValidation)
	}

	total, err := s.repo.SumForPeriod(ctx, from, to, filter)
	if err != nil {
		return 0, err
	}

	s.log.Info("рассчитана суммарная стоимость подписок",
		slog.String("from", model.FormatDate(from)),
		slog.String("to", model.FormatDate(to)),
		slog.Int64("total_price", total),
	)

	return total, nil
}
