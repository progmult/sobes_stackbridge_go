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
//
// Контекст ограничивает время миграции: без него недоступная база подвешивала
// бы старт сервиса бессрочно. Повторных попыток нет намеренно — их заменяет
// depends_on с healthcheck в compose и рестарт пода в kubernetes.
func Migrate(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("не удалось прочитать встроенные миграции: %w", err)
	}

	migrator, err := migrate.NewWithSourceInstance("iofs", source, cfg.DSN("pgx5"))
	if err != nil {
		return fmt.Errorf("не удалось инициализировать миграции: %w", err)
	}
	// golang-migrate не принимает контекст в Up, поэтому миграция уходит в
	// отдельную горутину, а отмена доводится до неё через GracefulStop.
	done := make(chan error, 1)

	go func() { done <- migrator.Up() }()

	select {
	case err := <-done:
		// Закрывать миграцию можно только здесь, когда Up() уже вернулся.
		// В defer это делать нельзя: на ветке отмены Up() продолжает работать,
		// и Close() выдернул бы из-под него источник и соединение.
		closeMigrator(migrator, log)

		if err != nil {
			if errors.Is(err, migrate.ErrNoChange) {
				log.Info("схема БД уже в актуальном состоянии")

				return nil
			}

			return fmt.Errorf("не удалось применить миграции: %w", err)
		}

	case <-ctx.Done():
		// Отправка не должна блокироваться: если миграция стоит на запросе,
		// читать из канала некому.
		select {
		case migrator.GracefulStop <- true:
		default:
		}

		// GracefulStop останавливает миграцию между файлами, а не посреди
		// зависшего запроса, поэтому дожидаться Up() здесь нечем. Соединение
		// остаётся горутине: после этой ошибки процесс завершается с кодом 1,
		// и живёт оно ровно до выхода.
		//
		// Контекст миграций выведен из сигнального, поэтому отмена приходит по
		// двум разным поводам: Ctrl-C и собственный таймаут. Формулировки
		// разные — иначе прерванный вручную запуск отчитывался бы о том, что
		// не уложился во время.
		if errors.Is(ctx.Err(), context.Canceled) {
			return fmt.Errorf("применение миграций прервано: %w", ctx.Err())
		}

		return fmt.Errorf("миграции не уложились в отведённое время: %w", ctx.Err())
	}

	log.Info("миграции применены")

	return nil
}

// closeMigrator освобождает источник миграций и соединение с базой. Close
// возвращает две ошибки — обе только логируем, на результат миграции они уже
// не влияют.
func closeMigrator(migrator *migrate.Migrate, log *slog.Logger) {
	sourceErr, dbErr := migrator.Close()
	if sourceErr != nil {
		log.Error("не удалось закрыть источник миграций", slog.String("error", sourceErr.Error()))
	}

	if dbErr != nil {
		log.Error("не удалось закрыть соединение миграций", slog.String("error", dbErr.Error()))
	}
}
