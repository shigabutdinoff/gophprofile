// Package web хранит одностраничное приложение загрузки и галереи аватарок.
package web

import _ "embed"

// Index это страница приложения, которую сервер отдаёт по веб адресам.
//
//go:embed static/index.html
var Index []byte
