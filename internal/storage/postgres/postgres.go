package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // драйвер схемы pgx5://
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"

	"sobes_stackbridge_go/internal/config"
	"sobes_stackbridge_go/migrations"
)

// NewPool создаёт пул соединений и проверяет доступность базы.
func NewPool(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, cfg.DSN("postgres"))
	if err != nil {
		return nil, fmt.Errorf("не удалось создать пул соединений с postgres: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()

		return nil, fmt.Errorf("база данных недоступна: %w", err)
	}

	return pool, nil
}

// Migrate накатывает миграции из migrations/ до последней версии.
// Повторный запуск ничего не меняет.
func Migrate(cfg *config.Config, log *slog.Logger) error {
	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("не удалось прочитать встроенные миграции: %w", err)
	}

	migrator, err := migrate.NewWithSourceInstance("iofs", source, cfg.DSN("pgx5"))
	if err != nil {
		return fmt.Errorf("не удалось инициализировать миграции: %w", err)
	}
	defer migrator.Close()

	if err := migrator.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			log.Info("схема БД уже в актуальном состоянии")

			return nil
		}

		return fmt.Errorf("не удалось применить миграции: %w", err)
	}

	log.Info("миграции применены")

	return nil
}
