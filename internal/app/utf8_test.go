package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestValidUTF8(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(validUTF8{sdktrace.NewSimpleSpanProcessor(exp)}))
	_, span := tp.Tracer("test").Start(context.Background(), "s",
		trace.WithAttributes(attribute.String("url.path", "/\xff"), attribute.Int("n", 1)))
	span.End()
	got := exp.GetSpans()[0].Attributes
	assert.Equal(t, []attribute.KeyValue{attribute.String("url.path", "/�"), attribute.Int("n", 1)}, got)
}
