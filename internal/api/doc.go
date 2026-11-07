// Package api переводит запросы REST API аватарок в сценарии пакета avatar.
package api

//go:generate go run github.com/swaggo/swag/cmd/swag@v1.16.6 init --dir .,../avatar --generalInfo doc.go --parseFuncBody --outputTypes json,yaml --output ../../api

// @title GophProfile
// @version 1.0
// @description Сервис хранит аватарки пользователей и раздаёт их другим сайтам. Пользователь передаётся
// @description в заголовке X-User-ID. С одного IP не больше 100 запросов в минуту, кроме /health.
// @license.name MIT
// @BasePath /
