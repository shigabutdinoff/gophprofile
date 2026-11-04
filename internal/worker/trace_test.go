package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutlog"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
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

func bridge(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	exp, err := stdoutlog.New(stdoutlog.WithWriter(&buf))
	require.NoError(t, err)
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exp)))
	prev := slog.Default()
	slog.SetDefault(slog.New(otelslog.NewHandler("test", otelslog.WithLoggerProvider(provider))))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestRecordsTrace(t *testing.T) {
	cases := map[string]struct {
		svc  *service
		want string
	}{
		"сбой на всех попытках": {&service{replies: []error{errNet}}, "событие ушло в очередь недоставленных"},
		"сбой и успех":          {&service{replies: []error{errNet, nil}}, "событие не обработано, повтор"},
		"повтор события":        {&service{replies: []error{avatar.ErrDone}}, "повтор события"},
		"статус не записан":     {&service{replies: []error{errNet}, failErr: errNet}, "статус сбоя не записан"},
	}
	for name, c := range cases {
		buf := bridge(t)
		consume(t, context.Background(), c.svc, event, amqp.Table{"traceparent": parent})
		assert.Contains(t, buf.String(), c.want, name)
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			var r struct{ TraceID string }
			require.NoError(t, json.Unmarshal([]byte(line), &r), name)
			assert.Equal(t, "0af7651916cd43dd8448eb211c80319c", r.TraceID, "%s: запись вне trace", name)
		}
	}
}
