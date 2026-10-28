// Package worker обрабатывает события загрузки и удаления аватарок из брокера.
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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/shigabutdinoff/gophprofile/internal/avatar"
)

// Service обрабатывает аватарки по событиям.
type Service interface {
	// HandleUploadEvent делает миниатюры аватарки по событию загрузки e.
	HandleUploadEvent(ctx context.Context, e avatar.AvatarUploadEvent) error
	// HandleDeleteEvent удаляет файлы аватарки по событию удаления e.
	HandleDeleteEvent(ctx context.Context, e avatar.AvatarDeleteEvent) error
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
	carrier := propagation.MapCarrier{}
	for k, v := range d.Headers {
		if s, ok := v.(string); ok {
			carrier[k] = s
		}
	}
	work, span := otel.Tracer("gophprofile").Start(otel.GetTextMapPropagator().Extract(work, carrier), "avatars.upload process",
		trace.WithSpanKind(trace.SpanKindConsumer), trace.WithAttributes(attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.destination.name", "avatars.upload")))
	defer span.End()
	var kind struct {
		S3Keys []string `json:"s3_keys"`
	}
	_ = json.Unmarshal(d.Body, &kind)
	upload := kind.S3Keys == nil
	id, call, err := decode(d.Body, upload, svc)
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
				slog.Warn("событие не обработано, повтор", "id", id, "попытка", n+1, "err", err)
			}),
		).Do(func() error {
			attempt, cancel := context.WithTimeout(work, limit)
			defer cancel()
			return call(attempt)
		})
	}
	switch {
	case errors.Is(err, avatar.ErrDone):
		slog.Info("повтор события", "id", id)
		err = d.Ack(false)
	case err == nil:
		err = d.Ack(false)
	case ctx.Err() != nil && !errors.Is(err, avatar.ErrPermanent):
		return
	default:
		slog.Error("событие ушло в очередь недоставленных", "id", id, "err", err)
		span.SetStatus(codes.Error, err.Error())
		if upload && id != "" {
			fctx, cancel := context.WithTimeout(work, limit)
			defer cancel()
			if ferr := svc.Fail(fctx, id); ferr != nil {
				slog.Error("статус сбоя не записан", "id", id, "err", ferr)
			}
		}
		err = d.Nack(false, false)
	}
	if err != nil {
		slog.Error("ответ брокеру не отправлен", "id", id, "err", err)
	}
}

func decode(body []byte, upload bool, svc Service) (string, func(context.Context) error, error) {
	if !upload {
		var e avatar.AvatarDeleteEvent
		err := json.Unmarshal(body, &e)
		return e.AvatarID, func(ctx context.Context) error { return svc.HandleDeleteEvent(ctx, e) }, err
	}
	var e avatar.AvatarUploadEvent
	err := json.Unmarshal(body, &e)
	return e.AvatarID, func(ctx context.Context) error { return svc.HandleUploadEvent(ctx, e) }, err
}
