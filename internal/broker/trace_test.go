package broker

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/shigabutdinoff/gophprofile/internal/avatar"
)

func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTracerProvider(noop.NewTracerProvider()) })
	return rec
}

func TestPublishClosedSpan(t *testing.T) {
	rec := recordSpans(t)
	b, err := Open("amqp://guest:guest@127.0.0.1:1/")
	require.NoError(t, err)
	require.NoError(t, b.Close())
	require.Error(t, b.Publish(context.Background(), exchange, key, avatar.AvatarUploadEvent{}))
	require.Len(t, rec.Ended(), 1)
	assert.Equal(t, "avatar.uploaded publish", rec.Ended()[0].Name())
	assert.Equal(t, codes.Error, rec.Ended()[0].Status().Code, "сбой брокера без ошибки в спане")
}

func (s *brokerSuite) TestTraceparent() {
	rec := recordSpans(s.T())
	ctx, parent := otel.Tracer("тест").Start(context.Background(), "запрос")
	s.Require().NoError(s.b.Publish(ctx, exchange, key, avatar.AvatarUploadEvent{AvatarID: "a7"}))
	parent.End()
	deliveries, err := s.b.Consume()
	s.Require().NoError(err)
	d := s.next(deliveries)
	s.Require().NoError(d.Ack(false))
	publish := rec.Ended()[0]
	s.Equal(parent.SpanContext().TraceID(), publish.SpanContext().TraceID())
	want := "00-" + publish.SpanContext().TraceID().String() + "-" + publish.SpanContext().SpanID().String() + "-01"
	s.Equal(want, d.Headers["traceparent"], "в заголовке не спан публикации")
}
