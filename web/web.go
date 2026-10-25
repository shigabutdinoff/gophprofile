// Package web хранит страницу загрузки аватарок из шаблона курса.
package web

import _ "embed"

// Index это страница загрузки аватарок, которую сервер отдаёт по корню.
//
//go:embed static/index.html
var Index []byte
