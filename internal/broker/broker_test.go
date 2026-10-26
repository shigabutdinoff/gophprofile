package broker

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
	"uuid"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/suite"
	"github.com/testcontainers/testcontainers-go"
	tcrabbit "github.com/testcontainers/testcontainers-go/modules/rabbitmq"

	"github.com/shigabutdinoff/gophprofile/internal/avatar"
)

type brokerSuite struct {
	suite.Suite
	url string
	b   *Broker
}

func TestBroker(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	suite.Run(t, new(brokerSuite))
}

func (s *brokerSuite) SetupSuite() {
	ctx := context.Background()
	ctr, err := tcrabbit.Run(ctx, "rabbitmq:4.4.0-management-alpine")
	testcontainers.CleanupContainer(s.T(), ctr)
	s.Require().NoError(err, "контейнер RabbitMQ не запущен")
	s.url, err = ctr.AmqpURL(ctx)
	s.Require().NoError(err, "адрес брокера не получен")
}

func (s *brokerSuite) SetupTest() {
	var err error
	s.b, err = Open(s.url)
	s.Require().NoError(err, "брокер не открыт")
	s.T().Cleanup(func() { _ = s.b.Close() })
	s.Require().NoError(s.b.Ping(context.Background()), "брокер не подключён")
	_, err = s.b.ch.QueuePurge(queue, false)
	s.Require().NoError(err)
	_, err = s.b.ch.QueuePurge(dlq, false)
	s.Require().NoError(err)
}

func (s *brokerSuite) next(deliveries <-chan amqp.Delivery) amqp.Delivery {
	select {
	case d := <-deliveries:
		return d
	case <-time.After(10 * time.Second):
		s.FailNow("событие не доставлено")
		return amqp.Delivery{}
	}
}

func (s *brokerSuite) TestPublishConsume() {
	e := avatar.AvatarUploadEvent{AvatarID: "a1", UserID: "alice", S3Key: "originals/a1"}
	s.Require().NoError(s.b.Publish(context.Background(), "avatars.exchange", "avatar.uploaded", e))
	s.Require().NoError(s.b.Publish(context.Background(), "avatars.exchange", "avatar.uploaded", e))
	deliveries, err := s.b.Consume()
	s.Require().NoError(err)
	d := s.next(deliveries)
	var got avatar.AvatarUploadEvent
	s.Require().NoError(json.Unmarshal(d.Body, &got))
	s.Equal(e, got)
	_, err = uuid.Parse(d.MessageId)
	s.Require().NoError(err, "идентификатор сообщения не UUID")
	s.Equal(amqp.Persistent, d.DeliveryMode)
	s.Require().NoError(d.Nack(false, false))
	again := s.next(deliveries)
	s.NotEqual(d.MessageId, again.MessageId, "идентификаторы сообщений совпали")
	s.Require().NoError(again.Ack(false))
	s.Eventually(func() bool {
		_, ok, err := s.b.ch.Get(dlq, true)
		return err == nil && ok
	}, 10*time.Second, 100*time.Millisecond, "событие не попало в очередь недоставленных")
}

func (s *brokerSuite) TestDeleteRouted() {
	e := avatar.AvatarDeleteEvent{AvatarID: "a6", S3Keys: []string{"originals/a6"}}
	s.Require().NoError(s.b.Publish(context.Background(), "avatars.exchange", "avatar.deleted", e))
	deliveries, err := s.b.Consume()
	s.Require().NoError(err)
	d := s.next(deliveries)
	s.Equal("avatar.deleted", d.RoutingKey)
	var got avatar.AvatarDeleteEvent
	s.Require().NoError(json.Unmarshal(d.Body, &got))
	s.Equal(e, got)
	s.Require().NoError(d.Ack(false))
}

func (s *brokerSuite) TestClosed() {
	s.Require().NoError(s.b.Ping(context.Background()))
	s.Require().NoError(s.b.Close())
	s.Error(s.b.Ping(context.Background()))
	s.Error(s.b.Publish(context.Background(), "avatars.exchange", "avatar.uploaded", avatar.AvatarUploadEvent{AvatarID: "a2"}))
}

func (s *brokerSuite) TestUnreachable() {
	b, err := Open("amqp://guest:guest@127.0.0.1:1/")
	s.Require().NoError(err)
	s.Error(b.Ping(context.Background()))
	s.Error(b.Publish(context.Background(), "avatars.exchange", "avatar.uploaded", avatar.AvatarUploadEvent{AvatarID: "a4"}))
}

func (s *brokerSuite) TestReconnect() {
	s.Require().NoError(s.b.conn.Close(), "соединение не разорвано")
	s.Require().NoError(s.b.Publish(context.Background(), "avatars.exchange", "avatar.uploaded", avatar.AvatarUploadEvent{AvatarID: "a5"}), "брокер не переподключился")
}

func (s *brokerSuite) TestReturnCounted() {
	s.Require().NoError(s.b.Publish(context.Background(), "avatars.exchange", "avatar.uploaded", avatar.AvatarUploadEvent{AvatarID: "a3"}))
	deliveries, err := s.b.Consume()
	s.Require().NoError(err)
	s.next(deliveries)
	s.Require().NoError(s.b.Close())
	b, err := Open(s.url)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = b.Close() })
	deliveries, err = b.Consume()
	s.Require().NoError(err)
	d := s.next(deliveries)
	s.Equal(int64(1), d.Headers["x-delivery-count"], "воркер читает счётчик возвратов как int64")
	s.Require().NoError(d.Ack(false))
}

func TestHungBroker(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }()
		}
	}()
	b, err := Open("amqp://guest:guest@" + l.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	first := make(chan error)
	go func() { first <- b.Ping(context.Background()) }()
	time.Sleep(100 * time.Millisecond)
	if err := b.Publish(context.Background(), exchange, key, avatar.AvatarUploadEvent{}); err != errDialing {
		t.Errorf("второй вызов: %v, ждали errDialing", err)
	}
	if time.Since(start) > time.Second {
		t.Error("второй вызов ждал подключения первого")
	}
	if err := <-first; err == nil || time.Since(start) > 3*dial {
		t.Errorf("первый вызов: %v за %v", err, time.Since(start))
	}
	_ = b.Close()
}
