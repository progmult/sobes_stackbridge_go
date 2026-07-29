// Package config читает настройки сервиса из переменных окружения.
package config

import (
	"fmt"
	"net/url"

	"github.com/ilyakaznacheev/cleanenv"
)

// Config — все настройки сервиса. Значения берутся из переменных окружения,
// которые docker compose подставляет из .env (см. .env.example).
type Config struct {
	HTTPPort string `env:"HTTP_PORT" env-default:"8080"`
	LogLevel string `env:"LOG_LEVEL" env-default:"info"`

	PostgresHost     string `env:"POSTGRES_HOST"     env-default:"localhost"`
	PostgresPort     string `env:"POSTGRES_PORT"     env-default:"5432"`
	PostgresUser     string `env:"POSTGRES_USER"     env-default:"postgres"`
	PostgresPassword string `env:"POSTGRES_PASSWORD" env-default:"postgres"`
	PostgresDB       string `env:"POSTGRES_DB"       env-default:"subscriptions"`
	PostgresSSLMode  string `env:"POSTGRES_SSLMODE"  env-default:"disable"`
}

// Load читает конфигурацию из окружения.
func Load() (*Config, error) {
	var cfg Config

	if err := cleanenv.ReadEnv(&cfg); err != nil {
		return nil, fmt.Errorf("не удалось прочитать конфигурацию из переменных окружения: %w", err)
	}

	return &cfg, nil
}

// DSN собирает строку подключения к PostgreSQL.
// Схему задаёт вызывающий: pgx ждёт "postgres", golang-migrate — "pgx5".
func (c *Config) DSN(scheme string) string {
	dsn := url.URL{
		Scheme:   scheme,
		User:     url.UserPassword(c.PostgresUser, c.PostgresPassword),
		Host:     c.PostgresHost + ":" + c.PostgresPort,
		Path:     "/" + c.PostgresDB,
		RawQuery: "sslmode=" + c.PostgresSSLMode,
	}

	return dsn.String()
}
