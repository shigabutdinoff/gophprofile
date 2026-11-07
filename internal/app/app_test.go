package app_test

import (
	"bytes"
	"context"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcrabbit "github.com/testcontainers/testcontainers-go/modules/rabbitmq"
	"go.opentelemetry.io/otel"

	"github.com/shigabutdinoff/gophprofile/internal/app"
)

func env(t *testing.T, rabbit string) {
	t.Helper()
	logger := slog.Default()
	t.Cleanup(func() {
		slog.SetDefault(logger)
		slog.SetLogLoggerLevel(slog.LevelInfo)
	})
	for key, value := range map[string]string{
		"DATABASE_URL":  "postgres://postgres:секрет-базы@127.0.0.1:1/postgres",
		"S3_ENDPOINT":   "http://127.0.0.1:1",
		"S3_ACCESS_KEY": "minio",
		"S3_SECRET_KEY": "секрет",
		"RABBITMQ_URL":  rabbit,
	} {
		t.Setenv(key, value)
	}
}

func TestOpenErrors(t *testing.T) {
	env(t, "amqp://guest:guest@127.0.0.1:1/")
	a, err := app.Open(context.Background())
	require.NoError(t, err, "без брокера сервер не стартует")
	a.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	a, err = app.Open(context.Background())
	require.NoError(t, err, "недоступный collector мешает запуску")
	started := time.Now()
	a.Close()
	assert.Less(t, time.Since(started), 6*time.Second, "трейсы отправляются дольше таймаута")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	for _, endpoint := range []string{"minio:9000", "http://", "::"} {
		t.Setenv("S3_ENDPOINT", endpoint)
		_, err = app.Open(context.Background())
		assert.ErrorContains(t, err, "S3_ENDPOINT", "адрес %q", endpoint)
	}
	env(t, "amqp://guest:Xk9%Qz/секрет-брокера@127.0.0.1:1/")
	_, err = app.Open(context.Background())
	require.ErrorContains(t, err, "RABBITMQ_URL")
	assert.NotContains(t, err.Error(), "секрет-брокера", "в ошибке пароль брокера")
	env(t, "amqp://guest:guest@127.0.0.1:1/")
	t.Setenv("DATABASE_URL", "postgres://postgres:секрет-базы@127.0.0.1:99999/postgres")
	_, err = app.Open(context.Background())
	require.ErrorContains(t, err, "DATABASE_URL")
	assert.NotContains(t, err.Error(), "секрет-базы", "в ошибке пароль базы")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("RABBITMQ_URL", "")
	_, err = app.Open(context.Background())
	assert.EqualError(t, err, "не заданы DATABASE_URL, RABBITMQ_URL")
}

func TestOpenHealth(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	ctr, err := tcrabbit.Run(ctx, "rabbitmq:4.4.0-management-alpine")
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err, "контейнер RabbitMQ не запущен")
	url, err := ctr.AmqpURL(ctx)
	require.NoError(t, err)
	env(t, url)
	a, err := app.Open(ctx)
	require.NoError(t, err)
	defer a.Close()
	rec := httptest.NewRecorder()
	a.Health().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "Postgres и S3 недоступны")
	assert.Contains(t, rec.Body.String(), `"rabbitmq":{"status":"up"`)
}

func output(t *testing.T, write func()) string {
	t.Helper()
	stdout := os.Stdout
	t.Cleanup(func() { os.Stdout = stdout })
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	a, err := app.Open(context.Background())
	require.NoError(t, err)
	write()
	a.Close()
	require.NoError(t, w.Close())
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(data)
}

func records() {
	slog.Info("инфо")
	slog.Warn("предупреждение")
	log.Print("журнал")
}

func TestOpenLogs(t *testing.T) {
	var mu sync.Mutex
	var sent []byte
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/v1/logs" {
			mu.Lock()
			sent = append(sent, body...)
			mu.Unlock()
		}
	}))
	defer collector.Close()
	env(t, "amqp://guest:guest@127.0.0.1:1/")
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", collector.URL+"/v1/logs")
	out := output(t, records)
	assert.NotContains(t, out, "инфо", "запись ниже порога")
	assert.Contains(t, out, `"level":"WARN","msg":"предупреждение"`)
	assert.Contains(t, out, `"level":"ERROR","msg":"журнал"`, "пакет log пишет уровнем ERROR")
	mu.Lock()
	assert.Empty(t, sent, "без OTEL_EXPORTER_OTLP_ENDPOINT мост отправляет записи")
	mu.Unlock()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	out = output(t, records)
	assert.Contains(t, out, "предупреждение", "запись не ушла в stdout")
	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, string(sent), "предупреждение", "мост не отправил запись")
	assert.NotContains(t, string(sent), "инфо", "мост отправил запись ниже порога")
}

func TestOpenLogLevel(t *testing.T) {
	env(t, "amqp://guest:guest@127.0.0.1:1/")
	t.Setenv("LOG_LEVEL", "DEBUG")
	assert.Contains(t, output(t, func() { slog.Debug("отладка") }), `"level":"DEBUG"`)
	t.Setenv("LOG_LEVEL", "")
	assert.NotContains(t, output(t, func() { slog.Debug("отладка") }), "отладка", "порог по умолчанию ниже info")
	t.Setenv("LOG_LEVEL", "warning")
	_, err := app.Open(context.Background())
	assert.ErrorContains(t, err, "LOG_LEVEL")
}

func TestOpenMetrics(t *testing.T) {
	var mu sync.Mutex
	var sent []byte
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/v1/metrics" {
			mu.Lock()
			sent = append(sent, body...)
			mu.Unlock()
		}
	}))
	defer collector.Close()
	provider := otel.GetMeterProvider()
	t.Cleanup(func() { otel.SetMeterProvider(provider) })
	env(t, "amqp://guest:guest@127.0.0.1:1/")
	output(t, func() {})
	mu.Lock()
	assert.Empty(t, sent, "без OTEL_EXPORTER_OTLP_ENDPOINT метрики отправлены")
	mu.Unlock()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	output(t, func() {})
	mu.Lock()
	assert.Contains(t, string(sent), "go.goroutine.count", "нет метрик runtime")
	assert.Contains(t, string(sent), "process.cpu.time", "нет CPU процесса")
	assert.Contains(t, string(sent), "pgxpool", "нет метрик пула")
	mu.Unlock()
	t.Setenv("OTEL_METRICS_EXPORTER", "nope")
	_, err := app.Open(context.Background())
	assert.ErrorContains(t, err, "OTEL_EXPORTER_OTLP_ENDPOINT")
}

func TestOpenLimits(t *testing.T) {
	for _, c := range []struct{ key, value string }{
		{"MAX_FILE_SIZE", "abc"},
		{"MAX_FILE_SIZE", "0"},
		{"MAX_FILE_SIZE", "10485761"},
		{"ALLOWED_MIME_TYPES", "image/png,image/gif"},
		{"ALLOWED_MIME_TYPES", "png"},
		{"TRUSTED_PROXIES", "abc"},
		{"TRUSTED_PROXIES", "-1"},
	} {
		t.Run(c.key+"="+c.value, func(t *testing.T) {
			env(t, "amqp://guest:guest@127.0.0.1:1/")
			t.Setenv(c.key, c.value)
			_, err := app.Open(context.Background())
			assert.ErrorContains(t, err, c.key, "значение %q", c.value)
		})
	}
}

func TestHandlerUploadLimit(t *testing.T) {
	env(t, "amqp://guest:guest@127.0.0.1:1/")
	t.Setenv("MAX_FILE_SIZE", "1048576")
	t.Setenv("ALLOWED_MIME_TYPES", "image/png")
	t.Setenv("TRUSTED_PROXIES", "1")
	a, err := app.Open(context.Background())
	require.NoError(t, err)
	t.Cleanup(a.Close)
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	resp, err := resty.New().R().SetHeader("X-User-ID", "alice").
		SetFileReader("file", "big.png", bytes.NewReader(make([]byte, 2<<20))).Post(srv.URL + "/api/v1/avatars")
	require.NoError(t, err)
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode())
	assert.JSONEq(t, `{"error":"File too large","max_size":1048576}`, resp.String())
}
