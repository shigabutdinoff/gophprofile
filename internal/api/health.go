package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexliesenfeld/health"
)

var errDown = errors.New("недоступен")

// Health проверяет службы checks по их именам при каждом запросе. Ответ
// 200, если все доступны, иначе 503. Текст ошибки службы пишется в журнал,
// а в ответ не попадает.
func Health(checks map[string]func(context.Context) error) http.Handler {
	opts := []health.CheckerOption{health.WithDisabledAutostart(), health.WithDisabledCache(), health.WithTimeout(time.Second)}
	for name, ping := range checks {
		opts = append(opts, health.WithCheck(health.Check{Name: name, Check: func(ctx context.Context) error {
			if err := ping(ctx); err != nil {
				slog.ErrorContext(ctx, "служба недоступна", "service", name, "err", err)
				return errDown
			}
			return nil
		}}))
	}
	return health.NewHandler(health.NewChecker(opts...))
}
