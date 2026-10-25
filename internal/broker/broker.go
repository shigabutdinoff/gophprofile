// Package broker передаёт события загрузки аватарок через RabbitMQ. Соединение
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
)

const (
	exchange = "avatars.exchange"
	key      = "avatar.uploaded"
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

// Broker публикует и принимает события загрузки.
type Broker struct {
	url    string
	mu     sync.Mutex // охраняет поля ниже, не держится во время подключения
	dial   sync.Mutex // занят, пока идёт подключение
	conn   *amqp.Connection
	ch     *amqp.Channel
	closed bool
}

// Open проверяет адрес url. Подключение к RabbitMQ и объявление очередей
// происходят при первом обращении.
func Open(url string) (*Broker, error) {
	if _, err := amqp.ParseURI(url); err != nil {
		return nil, errors.New("неверный адрес") // ошибка разбора содержит пароль
	}
	return &Broker{url: url}, nil
}

// channel отдаёт открытый канал, при разрыве подключаясь заново. Пока
// подключается один вызов, остальные сразу получают errDialing.
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

// connect подключается к url не дольше 2 с на соединение и столько же на
// handshake и объявляет очереди.
func connect(url string) (*amqp.Connection, *amqp.Channel, error) {
	conn, err := amqp.DialConfig(url, amqp.Config{Dial: amqp.DefaultDial(dial)})
	if err != nil {
		return nil, nil, err
	}
	ch, err := conn.Channel()
	if err == nil {
		err = errors.Join(ch.Confirm(false), ch.Qos(1, 0, false),
			ch.ExchangeDeclare(exchange, amqp.ExchangeDirect, true, false, false, false, nil),
			ch.ExchangeDeclare(dlx, amqp.ExchangeFanout, true, false, false, false, nil),
			declare(ch, dlq, nil),
			declare(ch, queue, amqp.Table{"x-queue-type": "quorum", "x-dead-letter-exchange": dlx, "x-delivery-limit": int32(5)}),
			ch.QueueBind(queue, key, exchange, false, nil), ch.QueueBind(dlq, "", dlx, false, nil))
	}
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return conn, ch, nil
}

func declare(ch *amqp.Channel, name string, args amqp.Table) error {
	_, err := ch.QueueDeclare(name, true, false, false, false, args)
	return err
}

// Publish отправляет event в exchange с ключом key под новым идентификатором
// сообщения и ждёт подтверждения брокера не дольше 5 с.
func (b *Broker) Publish(ctx context.Context, exchange, key string, event any) error {
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
		ContentType: "application/json", DeliveryMode: amqp.Persistent, MessageId: uuid.New().String(), Body: body})
	if err != nil {
		return err
	}
	ok, err := conf.WaitContext(ctx)
	if err == nil && !ok {
		err = errNack
	}
	return err
}

// Consume возвращает доставки событий загрузки с ручным подтверждением.
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
