// Package app собирает зависимости server и worker из переменных окружения
// DATABASE_URL, S3_ENDPOINT, S3_ACCESS_KEY, S3_SECRET_KEY, RABBITMQ_URL,
// LOG_LEVEL и OTEL_EXPORTER_OTLP_ENDPOINT, без неё телеметрия не уходит.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/instrumentation/host"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/contrib/processors/minsev"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
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
	logs   *sdklog.LoggerProvider
	meters *sdkmetric.MeterProvider
}

// Open проверяет переменные окружения, ставит логгер по умолчанию и
// открывает Postgres и S3. К RabbitMQ подключается при первом обращении.
func Open(ctx context.Context) (*App, error) {
	var level slog.Level
	if v := os.Getenv("LOG_LEVEL"); v != "" && level.UnmarshalText([]byte(v)) != nil {
		return nil, fmt.Errorf("неверный LOG_LEVEL %q", v)
	}
	stdout := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(stdout))
	slog.SetLogLoggerLevel(slog.LevelError)
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
	var logs sdklog.Exporter
	if err == nil {
		logs, err = autoexport.NewLogExporter(ctx)
	}
	var reader sdkmetric.Reader
	if err == nil {
		reader, err = autoexport.NewMetricReader(ctx)
	}
	if err != nil {
		a.Close()
		return nil, fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT: %w", err)
	}
	limits := sdktrace.NewSpanLimits()
	limits.AttributeValueLengthLimit = 4096 // заголовки клиента не раздувают пакет трейсов
	a.traces = sdktrace.NewTracerProvider(sdktrace.WithRawSpanLimits(limits), sdktrace.WithSpanProcessor(validUTF8{sdktrace.NewBatchSpanProcessor(exp)}))
	otel.SetTracerProvider(a.traces)
	a.logs = sdklog.NewLoggerProvider(sdklog.WithProcessor(minsev.NewLogProcessor(sdklog.NewBatchProcessor(logs), minsev.Severity(level))))
	slog.SetDefault(slog.New(slog.NewMultiHandler(stdout, otelslog.NewHandler("gophprofile", otelslog.WithLoggerProvider(a.logs)))))
	a.meters = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(a.meters)
	if err := errors.Join(runtime.Start(), host.Start(), otelpgx.RecordStats(db)); err != nil {
		a.Close()
		return nil, fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT: %w", err)
	}
	return a, nil
}

// MeterStorage отдаёт в метрики место, занятое аватарками пользователей.
func (a *App) MeterStorage() error {
	return postgres.New(a.db).ObserveStorage(otel.Meter("gophprofile"))
}

// Health отдаёт состояние Postgres, S3 и RabbitMQ.
func (a *App) Health() http.Handler {
	return api.Health(map[string]func(context.Context) error{"postgres": a.db.Ping, "s3": a.files.Ping, "rabbitmq": a.Broker.Ping})
}

// Close закрывает подключения и отправляет оставшиеся трейсы, логи и
// метрики не дольше 5 с.
func (a *App) Close() {
	_ = a.Broker.Close()
	a.db.Close()
	if a.traces != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.traces.Shutdown(ctx)
		_ = a.logs.Shutdown(ctx)
		_ = a.meters.Shutdown(ctx)
	}
}

// validUTF8 чинит строковые атрибуты спана из запроса: один невалидный байт
// UTF-8 ломает отправку всего пакета трейсов.
type validUTF8 struct{ sdktrace.SpanProcessor }

func (p validUTF8) OnStart(ctx context.Context, s sdktrace.ReadWriteSpan) {
	for _, kv := range s.Attributes() {
		if v := kv.Value.AsString(); kv.Value.Type() == attribute.STRING && !utf8.ValidString(v) {
			s.SetAttributes(kv.Key.String(strings.ToValidUTF8(v, "\uFFFD")))
		}
	}
	p.SpanProcessor.OnStart(ctx, s)
}
