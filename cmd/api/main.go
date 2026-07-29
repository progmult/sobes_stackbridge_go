// Сервис агрегации данных об онлайн-подписках пользователей.
package main

import (
	"context"
	"errors"
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

//	@title			Subscriptions API
//	@version		1.0
//	@description	REST-сервис для агрегации данных об онлайн-подписках пользователей.
//	@description	Даты передаются в формате MM-YYYY, стоимость — целое число рублей.

//	@host		localhost:8080
//	@BasePath	/api/v1

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("не удалось загрузить конфигурацию", slog.String("error", err.Error()))
		os.Exit(1)
	}

	log := newLogger(cfg.LogLevel)

	if err := postgres.Migrate(cfg, log); err != nil {
		log.Error("не удалось применить миграции", slog.String("error", err.Error()))
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.NewPool(ctx, cfg)
	if err != nil {
		log.Error("не удалось подключиться к базе данных", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer pool.Close()

	handler := rest.NewHandler(service.New(postgres.NewSubscriptionRepository(pool), log), pool, log)

	server := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           rest.NewRouter(handler, log),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("http-сервер запущен", slog.String("addr", server.Addr))

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("ошибка http-сервера", slog.String("error", err.Error()))
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("получен сигнал остановки")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error("ошибка при штатной остановке сервера", slog.String("error", err.Error()))
	}

	log.Info("сервис остановлен")
}

// newLogger настраивает структурированный логгер.
func newLogger(level string) *slog.Logger {
	var logLevel slog.Level
	if err := logLevel.UnmarshalText([]byte(level)); err != nil {
		logLevel = slog.LevelInfo
	}

	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
}
