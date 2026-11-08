package app

import (
	"context"
	"strings"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type validUTF8 struct{ sdktrace.SpanProcessor }

func (p validUTF8) OnStart(ctx context.Context, s sdktrace.ReadWriteSpan) {
	for _, kv := range s.Attributes() {
		if v := kv.Value.AsString(); kv.Value.Type() == attribute.STRING && !utf8.ValidString(v) {
			s.SetAttributes(kv.Key.String(strings.ToValidUTF8(v, "\uFFFD")))
		}
	}
	p.SpanProcessor.OnStart(ctx, s)
}
