// Сервис агрегации данных об онлайн-подписках пользователей.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sobes_stackbridge_go/internal/config"
	"sobes_stackbridge_go/internal/service"
	"sobes_stackbridge_go/internal/storage/postgres"
	"sobes_stackbridge_go/internal/transport/rest"
)

// Таймауты HTTP-сервера: без них медленный клиент может держать соединение
// сколько угодно.
const (
	readTimeout       = 15 * time.Second
	readHeaderTimeout = 10 * time.Second
	writeTimeout      = 15 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 10 * time.Second
	migrationTimeout  = 30 * time.Second
)

//	@title			Subscriptions API
//	@version		1.0
//	@description	REST-сервис для агрегации данных об онлайн-подписках пользователей.
//	@description	Даты передаются в формате MM-YYYY, стоимость — целое число рублей.

//	@host		localhost:8080
//	@BasePath	/api/v1

func main() {
	// Вся работа вынесена в run: только так ошибка старта доходит до os.Exit(1).
	// При обычном возврате из main процесс отдал бы код 0, и docker с kubernetes
	// сочли бы падение штатным завершением.
	if err := run(); err != nil {
		slog.Error("сервис остановлен с ошибкой", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("не удалось загрузить конфигурацию: %w", err)
	}

	log := newLogger(cfg.LogLevel)
	slog.SetDefault(log)

	// Контекст сигналов создаётся до миграций, чтобы Ctrl-C прерывал и их тоже.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	migrateCtx, cancelMigrate := context.WithTimeout(ctx, migrationTimeout)
	defer cancelMigrate()

	if err := postgres.Migrate(migrateCtx, cfg, log); err != nil {
		return fmt.Errorf("не удалось применить миграции: %w", err)
	}

	pool, err := postgres.NewPool(ctx, cfg)
	if err != nil {
		return fmt.Errorf("не удалось подключиться к базе данных: %w", err)
	}
	defer pool.Close()

	subscriptions := service.New(postgres.NewSubscriptionRepository(pool), log)
	handler := rest.NewHandler(subscriptions, pool, log)

	server := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           rest.NewRouter(handler, log),
		ReadTimeout:       readTimeout,
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	serverErrors := make(chan error, 1)

	go func() {
		log.Info("http-сервер запущен", slog.String("addr", server.Addr))

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	select {
	case err := <-serverErrors:
		return fmt.Errorf("http-сервер не смог принимать соединения: %w", err)

	case <-ctx.Done():
		log.Info("получен сигнал остановки")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("не удалось штатно остановить сервер: %w", err)
	}

	log.Info("сервис остановлен")

	return nil
}

// newLogger настраивает структурированный логгер.
func newLogger(level string) *slog.Logger {
	var logLevel slog.Level
	if err := logLevel.UnmarshalText([]byte(level)); err != nil {
		logLevel = slog.LevelInfo
	}

	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
}
