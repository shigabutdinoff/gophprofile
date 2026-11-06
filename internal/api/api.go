package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/shigabutdinoff/gophprofile/internal/avatar"
	"github.com/shigabutdinoff/gophprofile/web"
)

var errBadRequest = errors.New("неверный запрос")

type handler struct {
	svc   *avatar.Service
	limit int
}

// New собирает обработчик API над svc с загрузкой до size байт, веб
// приложением и состоянием health. С IP не больше 100 запросов в минуту, при
// proxies больше нуля IP берётся от прокси, пробы /live и /health без лимита.
func New(svc *avatar.Service, health http.Handler, size, proxies int) http.Handler {
	h := handler{svc: svc, limit: size}
	upload := counted(h.upload)
	r := chi.NewRouter()
	// Постоянное имя сервера держит Host клиента вне меток метрик.
	r.Use(otelhttp.NewMiddleware("http", otelhttp.WithServerName("gophprofile:8080"),
		otelhttp.WithFilter(func(r *http.Request) bool { return r.URL.Path != "/health" && r.URL.Path != "/live" })))
	h.routes(r, upload, health)
	limit := httprate.LimitBy(100, time.Minute, func(r *http.Request) (string, error) {
		return httprate.CanonicalizeIP(middleware.GetClientIP(r.Context())), nil
	})
	limited := limit(http.MaxBytesHandler(r, int64(h.limit)+(1<<20)))
	if proxies > 0 {
		limited = middleware.ClientIPFromXFFTrustedProxies(proxies)(limited)
	}
	limited = middleware.ClientIPFromRemoteAddr(limited)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/live" || req.URL.Path == "/health" {
			r.ServeHTTP(w, req)
			return
		}
		limited.ServeHTTP(w, req)
	})
}

func counted(next http.HandlerFunc) http.HandlerFunc {
	meter := otel.Meter("gophprofile")
	total, _ := meter.Int64Counter("avatars.uploads")
	took, _ := meter.Float64Histogram("avatars.upload.duration", metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10))
	return func(w http.ResponseWriter, r *http.Request) {
		// Время считается от последнего принятого байта тела, чтобы
		// медленный канал клиента не попадал во время ответа.
		start := time.Now()
		r.Body = lastRead{r.Body, &start}
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next(ww, r)
		status := "success"
		if ww.Status() >= http.StatusInternalServerError {
			status = "error"
		} else if ww.Status() >= http.StatusBadRequest {
			status = "rejected"
		}
		set := metric.WithAttributes(attribute.String("status", status))
		total.Add(r.Context(), 1, set)
		took.Record(r.Context(), time.Since(start).Seconds(), set)
	}
}

type lastRead struct {
	io.ReadCloser
	at *time.Time
}

func (b lastRead) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	*b.at = time.Now()
	return n, err
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
	case r.Context().Err() != nil:
		w.WriteHeader(499) // клиент ушёл, это не сбой сервиса
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
