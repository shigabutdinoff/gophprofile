package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophprofile/internal/avatar"
)

type acks struct {
	acked, nacked, requeued int
	fail                    error
}

func (a *acks) Ack(uint64, bool) error { a.acked++; return a.fail }

func (a *acks) Nack(_ uint64, _ bool, requeue bool) error {
	a.nacked++
	if requeue {
		a.requeued++
	}
	return a.fail
}

func (a *acks) Reject(uint64, bool) error { return nil }

type service struct {
	replies []error
	calls   int
	failed  []string
	failErr error
	onCall  func()
	got     avatar.AvatarUploadEvent
	deleted avatar.AvatarDeleteEvent
	ctx     context.Context
	failCtx context.Context
}

func (s *service) HandleDeleteEvent(ctx context.Context, e avatar.AvatarDeleteEvent) error {
	s.deleted = e
	return s.HandleUploadEvent(ctx, avatar.AvatarUploadEvent{})
}

func (s *service) HandleUploadEvent(ctx context.Context, e avatar.AvatarUploadEvent) error {
	s.calls++
	s.ctx = ctx
	s.got = e
	if s.onCall != nil {
		s.onCall()
	}
	if len(s.replies) == 0 {
		return nil
	}
	err := s.replies[0]
	if len(s.replies) > 1 {
		s.replies = s.replies[1:]
	}
	return err
}

func (s *service) Fail(ctx context.Context, id string) error {
	s.failCtx = ctx
	s.failed = append(s.failed, id)
	return s.failErr
}

type status503 struct{}

func (status503) Error() string       { return "503" }
func (status503) HTTPStatusCode() int { return 503 }

var errNet = &net.OpError{Op: "dial", Err: errors.New("отказ")}

func logs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func deliver(ctx context.Context, svc *service, body string) *acks {
	a := &acks{}
	handle(ctx, context.Background(), amqp.Delivery{Acknowledger: a, Body: []byte(body)}, svc, time.Millisecond, time.Second)
	return a
}

const event = `{"avatar_id":"a1","user_id":"alice","s3_key":"originals/a1"}`

func TestHandleOutcomes(t *testing.T) {
	cases := map[string]struct {
		replies       []error
		calls         int
		acked, nacked int
		failed        []string
	}{
		"успех":                 {nil, 1, 1, 0, nil},
		"повтор":                {[]error{avatar.ErrDone}, 1, 1, 0, nil},
		"сбой сети и успех":     {[]error{errNet, errNet, nil}, 3, 1, 0, nil},
		"ответ 503 и успех":     {[]error{status503{}, nil}, 2, 1, 0, nil},
		"обрыв Postgres":        {[]error{&pgconn.PgError{Code: pgerrcode.AdminShutdown}, nil}, 2, 1, 0, nil},
		"сбой на всех попытках": {[]error{io.EOF}, 6, 0, 1, []string{"a1"}},
		"постоянная ошибка":     {[]error{avatar.ErrPermanent}, 1, 0, 1, []string{"a1"}},
	}
	for name, c := range cases {
		svc := &service{replies: c.replies}
		a := deliver(context.Background(), svc, event)
		assert.Equal(t, c.calls, svc.calls, "%s: вызовы", name)
		assert.Equal(t, []int{c.acked, c.nacked, 0}, []int{a.acked, a.nacked, a.requeued}, "%s: ответ брокеру", name)
		assert.Equal(t, c.failed, svc.failed, "%s: отметка сбоя", name)
		assert.Equal(t, avatar.AvatarUploadEvent{AvatarID: "a1", UserID: "alice", S3Key: "originals/a1"}, svc.got, name)
	}
}

func TestHandleRepeatLogged(t *testing.T) {
	buf := logs(t)
	deliver(context.Background(), &service{replies: []error{avatar.ErrDone}}, event)
	assert.Contains(t, buf.String(), "повтор события")
}

func TestHandleBadEvent(t *testing.T) {
	svc := &service{}
	a := deliver(context.Background(), svc, "не json")
	assert.Zero(t, svc.calls, "битое событие дошло до сервиса")
	assert.Empty(t, svc.failed)
	assert.Equal(t, []int{0, 1, 0}, []int{a.acked, a.nacked, a.requeued})
}

func TestHandleStopDuringPause(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	svc := &service{replies: []error{errNet}, onCall: cancel}
	a := deliver(ctx, svc, event)
	assert.Equal(t, []int{0, 0}, []int{a.acked, a.nacked}, "остановленное событие подтверждено")
	assert.Empty(t, svc.failed)
}

func TestHandleFailures(t *testing.T) {
	buf := logs(t)
	svc := &service{replies: []error{avatar.ErrPermanent}, failErr: errors.New("база недоступна")}
	a := deliver(context.Background(), svc, event)
	assert.Equal(t, 1, a.nacked, "событие не ушло в очередь недоставленных")
	assert.Contains(t, buf.String(), "статус сбоя не записан")
	ack := &acks{fail: errors.New("неизвестный тег")}
	handle(context.Background(), context.Background(), amqp.Delivery{Acknowledger: ack, Body: []byte(event)}, &service{}, time.Millisecond, time.Second)
	assert.Contains(t, buf.String(), "ответ брокеру не отправлен")
}

func TestRun(t *testing.T) {
	deliveries := make(chan amqp.Delivery, 1)
	a := &acks{}
	deliveries <- amqp.Delivery{Acknowledger: a, Body: []byte(event)}
	close(deliveries)
	Run(context.Background(), deliveries, &service{}, time.Second)
	assert.Equal(t, 1, a.acked)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { Run(ctx, make(chan amqp.Delivery), &service{}, time.Second); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		require.Fail(t, "Run не остановился")
	}
}

func TestLost(t *testing.T) {
	assert.False(t, lost(context.Canceled))
	assert.False(t, lost(&pgconn.PgError{Code: pgerrcode.UniqueViolation}))
	assert.False(t, lost(errors.New("другое")))
	assert.True(t, lost(&pgconn.PgError{Code: pgerrcode.ConnectionFailure}))
	assert.True(t, lost(io.ErrUnexpectedEOF))
	assert.True(t, lost(fmt.Errorf("%w: ещё не помечена", avatar.ErrUnavailable)), "событие до пометки не повторено")
	assert.False(t, lost(context.DeadlineExceeded), "медленная обработка повторена")
}

func TestHandleReturnedTooOften(t *testing.T) {
	svc := &service{}
	a := &acks{}
	handle(context.Background(), context.Background(), amqp.Delivery{Acknowledger: a, Body: []byte(event),
		Headers: amqp.Table{"x-delivery-count": int64(4)}}, svc, time.Millisecond, time.Second)
	assert.Zero(t, svc.calls, "событие обработано после четырёх возвратов")
	assert.Equal(t, []string{"a1"}, svc.failed)
	assert.Equal(t, 1, a.nacked)
}

const deleteEvent = `{"avatar_id":"a1","s3_keys":["originals/a1","thumbnails/a1/100x100.jpg"]}`

func deliverDelete(svc *service, body string) *acks {
	a := &acks{}
	handle(context.Background(), context.Background(), amqp.Delivery{Acknowledger: a, Body: []byte(body), RoutingKey: "avatars.upload"},
		svc, time.Millisecond, time.Second)
	return a
}

func TestHandleDelete(t *testing.T) {
	cases := map[string]struct {
		replies       []error
		calls         int
		acked, nacked int
	}{
		"успех":                 {nil, 1, 1, 0},
		"повтор":                {[]error{avatar.ErrDone}, 1, 1, 0},
		"сбой сети и успех":     {[]error{errNet, nil}, 2, 1, 0},
		"сбой на всех попытках": {[]error{io.EOF}, 6, 0, 1},
	}
	for name, c := range cases {
		svc := &service{replies: c.replies}
		a := deliverDelete(svc, deleteEvent)
		assert.Equal(t, c.calls, svc.calls, "%s: вызовы", name)
		assert.Equal(t, []int{c.acked, c.nacked}, []int{a.acked, a.nacked}, "%s: ответ брокеру", name)
		assert.Empty(t, svc.failed, "%s: удалённая аватарка получила отметку сбоя", name)
		assert.Equal(t, avatar.AvatarDeleteEvent{AvatarID: "a1", S3Keys: []string{"originals/a1", "thumbnails/a1/100x100.jpg"}}, svc.deleted, name)
	}
}

func TestHandleBadDeleteEvent(t *testing.T) {
	svc := &service{}
	a := deliverDelete(svc, "не json")
	assert.Zero(t, svc.calls, "битое событие дошло до сервиса")
	assert.Equal(t, 1, a.nacked)
}
