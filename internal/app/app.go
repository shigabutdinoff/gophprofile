// Package app собирает зависимости программ server и worker из переменных
// окружения DATABASE_URL, S3_ENDPOINT, S3_ACCESS_KEY, S3_SECRET_KEY,
// RABBITMQ_URL и OTEL_EXPORTER_OTLP_ENDPOINT, без неё трейсы не уходят.
package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/shigabutdinoff/gophprofile/internal/api"
	"github.com/shigabutdinoff/gophprofile/internal/avatar"
	"github.com/shigabutdinoff/gophprofile/internal/broker"
	"github.com/shigabutdinoff/gophprofile/internal/postgres"
	"github.com/shigabutdinoff/gophprofile/internal/storage"
)

// App держит открытые подключения и сервис аватарок.
type App struct {
	// Service выполняет сценарии аватарок.
	Service *avatar.Service
	// Broker передаёт события загрузки.
	Broker *broker.Broker
	db     *pgxpool.Pool
	files  *storage.Client
	traces *sdktrace.TracerProvider
}

// Open проверяет переменные окружения и открывает Postgres и S3. К RabbitMQ
// подключается при первом обращении.
func Open(ctx context.Context) (*App, error) {
	var missing []string
	for _, key := range []string{"DATABASE_URL", "S3_ENDPOINT", "S3_ACCESS_KEY", "S3_SECRET_KEY", "RABBITMQ_URL"} {
		if os.Getenv(key) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("не заданы %s", strings.Join(missing, ", "))
	}
	endpoint := os.Getenv("S3_ENDPOINT")
	if u, err := url.Parse(endpoint); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("неверная S3_ENDPOINT %q, нужна схема %s или %s", endpoint, "http", "https")
	}
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL: %w", err)
	}
	cfg.ConnConfig.Tracer = otelpgx.NewTracer()
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL: %w", err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 64
	transport.ResponseHeaderTimeout = 30 * time.Second
	files := storage.New(&http.Client{Transport: transport}, endpoint, os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_SECRET_KEY"))
	b, err := broker.Open(os.Getenv("RABBITMQ_URL"))
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("RABBITMQ_URL: %w", err)
	}
	a := &App{Service: avatar.New(postgres.New(db), files, b), Broker: b, db: db, files: files}
	otel.SetTextMapPropagator(propagation.TraceContext{})
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return a, nil
	}
	exp, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		a.Close()
		return nil, fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT: %w", err)
	}
	a.traces = sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp))
	otel.SetTracerProvider(a.traces)
	return a, nil
}

// Health отдаёт состояние Postgres, S3 и RabbitMQ.
func (a *App) Health() http.Handler {
	return api.Health(map[string]func(context.Context) error{"postgres": a.db.Ping, "s3": a.files.Ping, "rabbitmq": a.Broker.Ping})
}

// Close закрывает подключения и отправляет оставшиеся трейсы не дольше 5 с.
func (a *App) Close() {
	_ = a.Broker.Close()
	a.db.Close()
	if a.traces != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.traces.Shutdown(ctx)
	}
}
