// Package postgres содержит подключение к PostgreSQL и хранилище подписок.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"sobes_stackbridge_go/internal/model"
	"sobes_stackbridge_go/internal/service"
)

const columns = `id, service_name, price, user_id, start_date, end_date`

// SubscriptionRepository — хранилище подписок в PostgreSQL.
type SubscriptionRepository struct {
	pool *pgxpool.Pool
}

// NewSubscriptionRepository создаёт хранилище подписок.
func NewSubscriptionRepository(pool *pgxpool.Pool) *SubscriptionRepository {
	return &SubscriptionRepository{pool: pool}
}

// Create сохраняет новую подписку и возвращает её вместе со сгенерированным id.
func (r *SubscriptionRepository) Create(ctx context.Context, sub *model.Subscription) (*model.Subscription, error) {
	const query = `
		INSERT INTO subscriptions (service_name, price, user_id, start_date, end_date)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING ` + columns

	row := r.pool.QueryRow(ctx, query, sub.ServiceName, sub.Price, sub.UserID, sub.StartDate, sub.EndDate)

	created, err := scan(row)
	if err != nil {
		return nil, fmt.Errorf("не удалось создать подписку: %w", err)
	}

	return created, nil
}

// GetByID возвращает подписку по идентификатору.
func (r *SubscriptionRepository) GetByID(ctx context.Context, id uuid.UUID) (*model.Subscription, error) {
	const query = `SELECT ` + columns + ` FROM subscriptions WHERE id = $1`

	sub, err := scan(r.pool.QueryRow(ctx, query, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, model.ErrNotFound
		}

		return nil, fmt.Errorf("не удалось получить подписку: %w", err)
	}

	return sub, nil
}

// Update перезаписывает поля подписки.
func (r *SubscriptionRepository) Update(ctx context.Context, sub *model.Subscription) (*model.Subscription, error) {
	const query = `
		UPDATE subscriptions
		SET service_name = $2, price = $3, user_id = $4, start_date = $5, end_date = $6
		WHERE id = $1
		RETURNING ` + columns

	row := r.pool.QueryRow(ctx, query, sub.ID, sub.ServiceName, sub.Price, sub.UserID, sub.StartDate, sub.EndDate)

	updated, err := scan(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, model.ErrNotFound
		}

		return nil, fmt.Errorf("не удалось обновить подписку: %w", err)
	}

	return updated, nil
}

// Delete удаляет подписку по идентификатору.
func (r *SubscriptionRepository) Delete(ctx context.Context, id uuid.UUID) error {
	const query = `DELETE FROM subscriptions WHERE id = $1`

	tag, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("не удалось удалить подписку: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return model.ErrNotFound
	}

	return nil
}

// List возвращает страницу подписок с необязательной фильтрацией по
// пользователю и названию сервиса, а также общее количество записей под
// фильтр — оно нужно клиенту, чтобы понимать, сколько ещё страниц впереди.
func (r *SubscriptionRepository) List(
	ctx context.Context,
	filter service.Filter,
	page service.Page,
) ([]model.Subscription, int, error) {
	where, args := buildFilter(filter, nil)

	countQuery := `SELECT count(*) FROM subscriptions` + where

	var total int
	if err := r.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("не удалось посчитать подписки: %w", err)
	}

	// id в сортировке делает порядок однозначным: без него записи с одинаковой
	// датой могли бы переставляться между страницами, теряясь при листании.
	listQuery := fmt.Sprintf(
		`SELECT %s FROM subscriptions%s ORDER BY start_date DESC, id LIMIT $%d OFFSET $%d`,
		columns, where, len(args)+1, len(args)+2,
	)

	rows, err := r.pool.Query(ctx, listQuery, append(args, page.Limit, page.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("не удалось получить список подписок: %w", err)
	}
	defer rows.Close()

	subscriptions := make([]model.Subscription, 0, page.Limit)

	for rows.Next() {
		sub, err := scan(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("не удалось разобрать строку подписки: %w", err)
		}

		subscriptions = append(subscriptions, *sub)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("ошибка при чтении списка подписок: %w", err)
	}

	return subscriptions, total, nil
}

// SumForPeriod считает суммарную стоимость подписок за период [from, to].
//
// Для каждой подписки берётся пересечение её срока действия с периодом,
// число месяцев в пересечении умножается на месячную стоимость. Подписка
// без даты окончания считается активной до конца периода.
func (r *SubscriptionRepository) SumForPeriod(
	ctx context.Context,
	from, to time.Time,
	filter service.Filter,
) (int64, error) {
	// Условия периода занимают $1 и $2, фильтры продолжают нумерацию с $3.
	// Здесь IS NULL — не «фильтр не задан», а настоящее бизнес-условие:
	// подписка без даты окончания действует до конца периода.
	conditions := []string{
		"start_date <= $2::date",
		"(end_date IS NULL OR end_date >= $1::date)",
	}

	where, args := buildFilter(filter, []any{from, to})
	if where != "" {
		conditions = append(conditions, strings.TrimPrefix(where, " WHERE "))
	}

	query := fmt.Sprintf(`
		WITH overlapping AS (
			SELECT price,
			       GREATEST(start_date, $1::date)                AS period_start,
			       LEAST(COALESCE(end_date, $2::date), $2::date) AS period_end
			FROM subscriptions
			WHERE %s
		)
		SELECT COALESCE(SUM(
			price::bigint * (
				(EXTRACT(YEAR FROM period_end)::int * 12 + EXTRACT(MONTH FROM period_end)::int)
			  - (EXTRACT(YEAR FROM period_start)::int * 12 + EXTRACT(MONTH FROM period_start)::int)
			  + 1
			)
		), 0)::bigint
		FROM overlapping`, strings.Join(conditions, " AND "))

	var total int64

	err := r.pool.QueryRow(ctx, query, args...).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("не удалось посчитать суммарную стоимость: %w", err)
	}

	return total, nil
}

// buildFilter собирает условие WHERE только из тех фильтров, которые заданы,
// и дописывает их значения к args, продолжая нумерацию плейсхолдеров.
//
// Условие для незаданного фильтра не добавляется вовсе — и это принципиально.
// Универсальный вариант «$1 IS NULL OR user_id = $1» позволил бы обойтись
// статическим SQL, но на generic-плане, когда значение параметра планировщику
// неизвестно, он обязан построить план, годный для обеих веток, и индексом
// воспользоваться уже не может: выборка вырождается в перебор всей таблицы.
//
// В SQL подставляются только номера плейсхолдеров, значения всегда уходят
// параметрами — на инъекции это не влияет.
func buildFilter(filter service.Filter, args []any) (string, []any) {
	var conditions []string

	if filter.UserID != nil {
		args = append(args, *filter.UserID)
		conditions = append(conditions, fmt.Sprintf("user_id = $%d", len(args)))
	}

	if filter.ServiceName != nil {
		args = append(args, *filter.ServiceName)
		conditions = append(conditions, fmt.Sprintf("lower(service_name) = lower($%d::text)", len(args)))
	}

	if len(conditions) == 0 {
		return "", args
	}

	return " WHERE " + strings.Join(conditions, " AND "), args
}

// scanner объединяет pgx.Row и pgx.Rows: обе умеют Scan.
type scanner interface {
	Scan(dest ...any) error
}

func scan(row scanner) (*model.Subscription, error) {
	var sub model.Subscription

	err := row.Scan(&sub.ID, &sub.ServiceName, &sub.Price, &sub.UserID, &sub.StartDate, &sub.EndDate)
	if err != nil {
		return nil, err
	}

	return &sub, nil
}
