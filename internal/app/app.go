// Package app собирает зависимости программ server и worker из переменных
// окружения DATABASE_URL, S3_ENDPOINT, S3_ACCESS_KEY, S3_SECRET_KEY и
// RABBITMQ_URL.
package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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
	db, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
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
	return &App{avatar.New(postgres.New(db), files, b), b, db, files}, nil
}

// Health отдаёт состояние Postgres, S3 и RabbitMQ.
func (a *App) Health() http.Handler {
	return api.Health(map[string]func(context.Context) error{"postgres": a.db.Ping, "s3": a.files.Ping, "rabbitmq": a.Broker.Ping})
}

// Close закрывает подключения.
func (a *App) Close() {
	_ = a.Broker.Close()
	a.db.Close()
}
