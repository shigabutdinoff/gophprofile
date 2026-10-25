// Package worker делает миниатюры аватарок по событиям загрузки из брокера.
// Временный сбой повторяется с растущей паузой, а событие, которое не
// удалось обработать, уходит в очередь недоставленных.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/avast/retry-go/v5"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/shigabutdinoff/gophprofile/internal/avatar"
)

// Service обрабатывает аватарки по событиям.
type Service interface {
	// HandleUploadEvent делает миниатюры аватарки по событию загрузки e.
	HandleUploadEvent(ctx context.Context, e avatar.AvatarUploadEvent) error
	// Fail отмечает, что аватарку id обработать не удалось.
	Fail(ctx context.Context, id string) error
}

// Run обрабатывает доставки deliveries сервисом svc, пока не отменён ctx
// или не закрыт канал. Попытка длится не дольше 30 с. После отмены событие
// доводится не дольше grace, а неподтверждённое брокер доставит снова.
func Run(ctx context.Context, deliveries <-chan amqp.Delivery, svc Service, grace time.Duration) {
	work, cancel := graceful(ctx, grace)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case d, ok := <-deliveries:
			if !ok {
				return
			}
			handle(ctx, work, d, svc, 250*time.Millisecond, 30*time.Second)
		}
	}
}

func handle(ctx, work context.Context, d amqp.Delivery, svc Service, pause, limit time.Duration) {
	var e avatar.AvatarUploadEvent
	err := json.Unmarshal(d.Body, &e)
	returned, _ := d.Headers["x-delivery-count"].(int64)
	switch {
	case err != nil:
		err = fmt.Errorf("%w: %w", avatar.ErrPermanent, err)
	case returned >= 4:
		err = fmt.Errorf("%w: событие возвращалось в очередь %d раз", avatar.ErrPermanent, returned)
	default:
		err = retry.New(retry.Context(ctx), retry.Attempts(6), retry.Delay(pause), retry.MaxDelay(8*pause),
			retry.DelayType(retry.BackOffDelay), retry.LastErrorOnly(true), retry.RetryIf(lost),
			retry.OnRetry(func(n uint, err error) {
				slog.Warn("событие не обработано, повтор", "id", e.AvatarID, "попытка", n+1, "err", err)
			}),
		).Do(func() error {
			attempt, cancel := context.WithTimeout(work, limit)
			defer cancel()
			return svc.HandleUploadEvent(attempt, e)
		})
	}
	switch {
	case errors.Is(err, avatar.ErrDone):
		slog.Info("повтор события", "id", e.AvatarID)
		err = d.Ack(false)
	case err == nil:
		err = d.Ack(false)
	case ctx.Err() != nil && !errors.Is(err, avatar.ErrPermanent):
		return
	default:
		slog.Error("событие ушло в очередь недоставленных", "id", e.AvatarID, "err", err)
		if e.AvatarID != "" {
			fctx, cancel := context.WithTimeout(work, limit)
			defer cancel()
			if ferr := svc.Fail(fctx, e.AvatarID); ferr != nil {
				slog.Error("статус сбоя не записан", "id", e.AvatarID, "err", ferr)
			}
		}
		err = d.Nack(false, false)
	}
	if err != nil {
		slog.Error("ответ брокеру не отправлен", "id", e.AvatarID, "err", err)
	}
}
