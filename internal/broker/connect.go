package broker

import (
	"errors"

	amqp "github.com/rabbitmq/amqp091-go"
)

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
			ch.QueueBind(queue, key, exchange, false, nil), ch.QueueBind(queue, deleted, exchange, false, nil),
			ch.QueueBind(dlq, "", dlx, false, nil))
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
