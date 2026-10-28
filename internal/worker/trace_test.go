package worker

import (
	"context"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/shigabutdinoff/gophprofile/internal/avatar"
)

const parent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"

func consume(t *testing.T, ctx context.Context, svc *service, body string, headers amqp.Table) sdktrace.ReadOnlySpan {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTracerProvider(noop.NewTracerProvider()) })
	handle(ctx, context.Background(), amqp.Delivery{Acknowledger: &acks{}, Body: []byte(body), Headers: headers},
		svc, time.Millisecond, time.Second)
	require.Len(t, rec.Ended(), 1, "ожидался один спан обработки")
	return rec.Ended()[0]
}

func TestConsumerSpan(t *testing.T) {
	for name, body := range map[string]string{"загрузка": event, "удаление": deleteEvent} {
		svc := &service{}
		span := consume(t, context.Background(), svc, body, amqp.Table{"traceparent": parent})
		assert.Equal(t, "avatars.upload process", span.Name(), name)
		assert.Equal(t, trace.SpanKindConsumer, span.SpanKind(), name)
		assert.Equal(t, "b7ad6b7169203331", span.Parent().SpanID().String(), "%s: спан не дочерний к публикации", name)
		assert.Equal(t, span.SpanContext(), trace.SpanContextFromContext(svc.ctx), "%s: попытка вне trace", name)
		assert.Equal(t, codes.Unset, span.Status().Code, name)
	}
}

func TestConsumerSpanHeaders(t *testing.T) {
	for name, headers := range map[string]amqp.Table{
		"без заголовков":    nil,
		"битый traceparent": {"traceparent": "мусор"},
		"счётчик возвратов": {"x-delivery-count": int64(1)},
	} {
		span := consume(t, context.Background(), &service{}, event, headers)
		assert.False(t, span.Parent().IsValid(), "%s: у нового trace есть родитель", name)
	}
}

func TestConsumerSpanStatus(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := map[string]struct {
		ctx     context.Context
		replies []error
		headers amqp.Table
		want    codes.Code
	}{
		"сбой и успех":          {context.Background(), []error{errNet, nil}, nil, codes.Unset},
		"повтор события":        {context.Background(), []error{avatar.ErrDone}, nil, codes.Unset},
		"остановка worker":      {canceled, []error{errNet}, nil, codes.Unset},
		"четыре возврата":       {context.Background(), nil, amqp.Table{"x-delivery-count": int64(4), "traceparent": parent}, codes.Error},
		"сбой на всех попытках": {context.Background(), []error{errNet}, nil, codes.Error},
	}
	for name, c := range cases {
		svc := &service{replies: c.replies}
		span := consume(t, c.ctx, svc, event, c.headers)
		assert.Equal(t, c.want, span.Status().Code, name)
		if svc.failCtx != nil {
			assert.Equal(t, span.SpanContext(), trace.SpanContextFromContext(svc.failCtx), "%s: отметка сбоя вне trace", name)
		}
	}
}
