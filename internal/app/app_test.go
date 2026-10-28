package app_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcrabbit "github.com/testcontainers/testcontainers-go/modules/rabbitmq"

	"github.com/shigabutdinoff/gophprofile/internal/app"
)

func env(t *testing.T, rabbit string) {
	t.Helper()
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
