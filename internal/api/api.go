// Package api переводит запросы REST API аватарок в сценарии пакета avatar.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httprate"

	"github.com/shigabutdinoff/gophprofile/internal/avatar"
	"github.com/shigabutdinoff/gophprofile/web"
)

var errBadRequest = errors.New("неверный запрос")

type handler struct {
	svc *avatar.Service
}

// New собирает обработчик маршрутов загрузки, чтения и удаления аватарок
// над svc и веб приложения. Состояние служб отдаёт health.
// С одного IP принимается не больше 100 запросов в минуту.
func New(svc *avatar.Service, health http.Handler) http.Handler {
	h := handler{svc: svc}
	r := chi.NewRouter()
	r.Method(http.MethodGet, "/health", health)
	r.Get("/", spa)
	r.Get("/web/upload", spa)
	r.Post("/web/upload", h.upload)
	r.Get("/web/gallery/*", spa)
	r.Post("/api/v1/avatars", h.upload)
	r.Get("/api/v1/avatars/{id}", h.file)
	r.Get("/api/v1/avatars/{id}/metadata", h.metadata)
	r.Get("/api/v1/users/{user_id}/avatars", h.list)
	r.Get("/api/v1/users/{user_id}/avatar", h.file)
	r.Delete("/api/v1/avatars/{id}", h.remove)
	r.Delete("/api/v1/users/{user_id}/avatar", h.remove)
	return httprate.LimitByIP(100, time.Minute)(http.MaxBytesHandler(r, avatar.MaxSize+(1<<20)))
}

func spa(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(web.Index) }

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func respond(w http.ResponseWriter, r *http.Request, body any, err error) {
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, avatar.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Avatar not found"})
	case errors.Is(err, avatar.ErrForbidden):
		writeJSON(w, http.StatusForbidden, struct {
			Error   string `json:"error"`
			Details string `json:"details"`
		}{"Forbidden", "You can only delete your own avatars"})
	case errors.Is(err, avatar.ErrInvalidFormat):
		writeJSON(w, http.StatusBadRequest, struct {
			Error   string `json:"error"`
			Details string `json:"details"`
		}{"Invalid file format", "Supported formats: jpeg, png, webp"})
	case errors.Is(err, avatar.ErrTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "File too large", "max_size": avatar.MaxSize})
	case errors.Is(err, avatar.ErrUserID), errors.Is(err, errBadRequest), errors.Is(err, avatar.ErrTooManyPixels):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, avatar.ErrUnavailable):
		slog.ErrorContext(r.Context(), "событие не принято", "path", r.URL.Path, "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": avatar.ErrUnavailable.Error()})
	default:
		slog.ErrorContext(r.Context(), "запрос не выполнен", "method", r.Method, "path", r.URL.Path, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "внутренняя ошибка"})
	}
}
