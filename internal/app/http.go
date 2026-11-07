package app

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel"

	"github.com/shigabutdinoff/gophprofile/internal/api"
	"github.com/shigabutdinoff/gophprofile/internal/postgres"
)

// MeterStorage отдаёт в метрики место, занятое аватарками пользователей.
func (a *App) MeterStorage() error {
	return postgres.New(a.db).ObserveStorage(otel.Meter("gophprofile"))
}

// Health отдаёт состояние Postgres, S3 и RabbitMQ.
func (a *App) Health() http.Handler {
	return api.Health(map[string]func(context.Context) error{"postgres": a.db.Ping, "s3": a.files.Ping, "rabbitmq": a.Broker.Ping})
}

// Handler собирает HTTP обработчик REST API, веб приложения и проверки
// состояния над сервисом аватарок.
func (a *App) Handler() http.Handler {
	return api.New(a.Service, a.Health(), a.size, a.trust)
}
