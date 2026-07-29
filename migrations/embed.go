// Package migrations содержит SQL-миграции схемы БД и встраивает их в бинарь,
// чтобы сервис мог накатывать их при старте без внешних файлов.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
