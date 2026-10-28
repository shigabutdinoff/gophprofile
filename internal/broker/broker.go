// Package broker передаёт события аватарок через RabbitMQ. Соединение
// открывается при первом обращении и переоткрывается после разрыва.
package broker

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
	"uuid"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	exchange = "avatars.exchange"
	key      = "avatar.uploaded"
	deleted  = "avatar.deleted"
	queue    = "avatars.upload"
	dlx      = "avatars.dlx"
	dlq      = "avatars.upload.dlq"
	confirm  = 5 * time.Second
	dial     = 2 * time.Second
)

var (
	errNack, errClosed = errors.New("брокер отклонил событие"), errors.New("соединение с брокером закрыто")
	errDialing         = errors.New("подключение к брокеру уже идёт")
)

// Broker публикует и принимает события загрузки и удаления.
type Broker struct {
	url    string
	mu     sync.Mutex
	dial   sync.Mutex
	conn   *amqp.Connection
	ch     *amqp.Channel
	closed bool
}

// Open проверяет адрес url. Подключение к RabbitMQ и объявление очередей
// происходят при первом обращении.
func Open(url string) (*Broker, error) {
	if _, err := amqp.ParseURI(url); err != nil {
		return nil, errors.New("неверный адрес")
	}
	return &Broker{url: url}, nil
}

func (b *Broker) channel() (*amqp.Channel, error) {
	b.mu.Lock()
	ch, closed := b.ch, b.closed
	b.mu.Unlock()
	if closed {
		return nil, errClosed
	}
	if ch != nil && !ch.IsClosed() {
		return ch, nil
	}
	if !b.dial.TryLock() {
		return nil, errDialing
	}
	defer b.dial.Unlock()
	conn, ch, err := connect(b.url)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		_ = conn.Close()
		return nil, errClosed
	}
	if b.conn != nil {
		_ = b.conn.Close()
	}
	b.conn, b.ch = conn, ch
	return ch, nil
}

// Publish отправляет event в exchange с ключом key под новым идентификатором
// сообщения и ждёт подтверждения брокера не дольше 5 с. Контекст трассировки
// ctx уходит в заголовках сообщения.
func (b *Broker) Publish(ctx context.Context, exchange, key string, event any) (err error) {
	ctx, span := otel.Tracer("gophprofile").Start(ctx, key+" publish", trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(attribute.String("messaging.system", "rabbitmq"), attribute.String("messaging.destination.name", exchange)))
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	ch, err := b.channel()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, confirm)
	defer cancel()
	conf, err := ch.PublishWithDeferredConfirmWithContext(ctx, exchange, key, false, false, amqp.Publishing{
		ContentType: "application/json", DeliveryMode: amqp.Persistent, MessageId: uuid.New().String(), Body: body, Headers: headers(ctx)})
	if err != nil {
		return err
	}
	ok, err := conf.WaitContext(ctx)
	if err == nil && !ok {
		err = errNack
	}
	return err
}

// Consume возвращает доставки событий с ручным подтверждением.
// Канал доставок закрывается при разрыве соединения.
func (b *Broker) Consume() (<-chan amqp.Delivery, error) {
	ch, err := b.channel()
	if err != nil {
		return nil, err
	}
	return ch.Consume(queue, "", false, false, false, false, nil)
}

// Ping сообщает, открыто ли соединение с брокером, и переоткрывает разорванное.
func (b *Broker) Ping(context.Context) error {
	_, err := b.channel()
	return err
}

// Close закрывает соединение с брокером, после него обращения дают ошибку.
func (b *Broker) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	if b.conn == nil {
		return nil
	}
	return b.conn.Close()
}
