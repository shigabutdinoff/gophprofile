package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (h handler) routes(r chi.Router, upload http.HandlerFunc, health http.Handler) {
	// @Summary Состояние сервиса
	// @Description Проверяет Postgres, S3 и RabbitMQ. 503, если хотя бы одна служба недоступна.
	// @Tags health
	// @Produce json
	// @Success 200 {object} object{status=string,details=object} "Все службы доступны"
	// @Failure 503 {object} object{status=string,details=object} "Служба недоступна"
	// @Router /health [get]
	r.Method(http.MethodGet, "/health", health)
	r.Get("/live", func(http.ResponseWriter, *http.Request) {})
	r.Get("/", spa)
	// @Summary Страница загрузки
	// @Tags web
	// @Produce html,plain
	// @Success 200 {string} string "Веб приложение"
	// @Failure 429 {string} string "Больше 100 запросов в минуту"
	// @Router /web/upload [get]
	r.Get("/web/upload", spa)
	r.Post("/web/upload", upload)
	// @Summary Галерея пользователя
	// @Tags web
	// @Produce html,plain
	// @Param user_id path string true "Пользователь"
	// @Success 200 {string} string "Веб приложение"
	// @Failure 429 {string} string "Больше 100 запросов в минуту"
	// @Router /web/gallery/{user_id} [get]
	r.Get("/web/gallery/*", spa)
	// @Summary Загрузка аватарки
	// @Description Принимает jpeg, png или webp до 10 МБ в поле file или image формы.
	// @Tags avatars
	// @Accept mpfd
	// @Produce json,plain
	// @Param X-User-ID header string true "Владелец"
	// @Param file formData file false "Картинка"
	// @Param image formData file false "Картинка, если нет file"
	// @Success 201 {object} created "Аватарка принята, миниатюры в работе"
	// @Failure 400,500,503 {object} object{error=string,details=string} "Неверный запрос или сбой"
	// @Failure 413 {object} object{error=string,max_size=int} "Файл больше лимита"
	// @Failure 429 {string} string "Больше 100 запросов в минуту"
	// @Router /api/v1/avatars [post]
	r.Post("/api/v1/avatars", upload)
	// @Summary Файл аватарки
	// @Description Оригинал или миниатюра, при format в другом формате. Кэш на сутки.
	// @Tags avatars
	// @Produce png,jpeg,image/webp,json,plain
	// @Param id path string true "Аватарка"
	// @Param size query string false "Размер" Enums(original,100x100,300x300)
	// @Param format query string false "Формат" Enums(jpeg,png,webp)
	// @Success 200 {file} file "Картинка"
	// @Header 200 {string} ETag "Хеш содержимого"
	// @Header 200 {string} Cache-Control "max-age=86400"
	// @Success 304 "Не изменилась с If-None-Match"
	// @Failure 400,404,500 {object} object{error=string,details=string} "Неверный запрос, нет аватарки или сбой"
	// @Failure 429 {string} string "Больше 100 запросов в минуту"
	// @Router /api/v1/avatars/{id} [get]
	r.Get("/api/v1/avatars/{id}", h.file)
	// @Summary Метаданные аватарки
	// @Tags avatars
	// @Produce json,plain
	// @Param id path string true "Аватарка"
	// @Success 200 {object} metadata "Размеры, формат, статус и миниатюры"
	// @Failure 404,500 {object} object{error=string,details=string} "Нет аватарки или сбой"
	// @Failure 429 {string} string "Больше 100 запросов в минуту"
	// @Router /api/v1/avatars/{id}/metadata [get]
	r.Get("/api/v1/avatars/{id}/metadata", h.metadata)
	// @Summary Аватарки пользователя
	// @Tags users
	// @Produce json,plain
	// @Param user_id path string true "Пользователь"
	// @Success 200 {array} metadata "Все аватарки, новые первыми"
	// @Failure 400,500 {object} object{error=string,details=string} "Неверный запрос или сбой"
	// @Failure 429 {string} string "Больше 100 запросов в минуту"
	// @Router /api/v1/users/{user_id}/avatars [get]
	r.Get("/api/v1/users/{user_id}/avatars", h.list)
	// @Summary Текущая аватарка
	// @Description Последняя загруженная аватарка пользователя, без кэша.
	// @Tags users
	// @Produce png,jpeg,image/webp,json,plain
	// @Param user_id path string true "Пользователь"
	// @Param size query string false "Размер" Enums(original,100x100,300x300)
	// @Param format query string false "Формат" Enums(jpeg,png,webp)
	// @Success 200 {file} file "Картинка"
	// @Header 200 {string} ETag "Хеш содержимого"
	// @Header 200 {string} Cache-Control "no-cache"
	// @Success 304 "Не изменилась с If-None-Match"
	// @Failure 400,404,500 {object} object{error=string,details=string} "Неверный запрос, нет аватарки или сбой"
	// @Failure 429 {string} string "Больше 100 запросов в минуту"
	// @Router /api/v1/users/{user_id}/avatar [get]
	r.Get("/api/v1/users/{user_id}/avatar", h.file)
	// @Summary Удаление аватарки
	// @Description Удаляет свою аватарку вместе с миниатюрами.
	// @Tags avatars
	// @Produce json,plain
	// @Param X-User-ID header string true "Владелец"
	// @Param id path string true "Аватарка"
	// @Success 204 "Удалена"
	// @Failure 400,403,404,500,503 {object} object{error=string,details=string} "Чужая, нет аватарки или сбой"
	// @Failure 429 {string} string "Больше 100 запросов в минуту"
	// @Router /api/v1/avatars/{id} [delete]
	r.Delete("/api/v1/avatars/{id}", h.remove)
	// @Summary Удаление текущей аватарки
	// @Tags users
	// @Produce json,plain
	// @Param X-User-ID header string true "Владелец"
	// @Param user_id path string true "Пользователь"
	// @Success 204 "Удалена"
	// @Failure 400,403,404,500,503 {object} object{error=string,details=string} "Чужая, нет аватарки или сбой"
	// @Failure 429 {string} string "Больше 100 запросов в минуту"
	// @Router /api/v1/users/{user_id}/avatar [delete]
	r.Delete("/api/v1/users/{user_id}/avatar", h.remove)
}
