package api_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/shigabutdinoff/gophprofile/internal/api"
)

func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(noop.NewTracerProvider()) })
	return rec
}

func ended(t *testing.T, rec *tracetest.SpanRecorder, n int) []sdktrace.ReadOnlySpan {
	t.Helper()
	var spans []sdktrace.ReadOnlySpan
	assert.Eventually(t, func() bool {
		spans = nil
		for _, s := range rec.Ended() {
			if s.SpanKind() == trace.SpanKindServer {
				spans = append(spans, s)
			}
		}
		return len(spans) >= n
	}, time.Second, 10*time.Millisecond, "спаны не закрыты")
	return spans
}

func names(spans []sdktrace.ReadOnlySpan) []string {
	var got []string
	for _, s := range spans {
		got = append(got, s.Name())
	}
	return got
}

func TestSpanNames(t *testing.T) {
	rec := recordSpans(t)
	srv, _ := start(t)
	id := idOf(upload(t, srv, "alice", "a.png", pngBytes(t, 2, 2, 0)))
	send(t, resty.New().R().SetFileReader("file", "a.png", bytes.NewReader(pngBytes(t, 2, 2, 0))).SetHeader("X-User-ID", "alice"), http.MethodPost, srv.URL+"/web/upload")
	get(t, srv, "/api/v1/avatars/"+id)
	remove(t, srv, "alice", "/api/v1/avatars/"+id)
	remove(t, srv, "alice", "/api/v1/users/alice/avatar")
	assert.Equal(t, []string{"POST /api/v1/avatars", "POST /web/upload", "GET /api/v1/avatars/{id}",
		"DELETE /api/v1/avatars/{id}", "DELETE /api/v1/users/{user_id}/avatar"}, names(ended(t, rec, 5)))
}

func TestHealthNotTraced(t *testing.T) {
	rec := recordSpans(t)
	ok := func(context.Context) error { return nil }
	srv := httptest.NewServer(api.New(nil, api.Health(map[string]func(context.Context) error{"postgres": ok})))
	t.Cleanup(srv.Close)
	get(t, srv, "/health")
	assert.Empty(t, rec.Ended(), "проверка состояния попала в трейсы")
}

func TestSpanStatus(t *testing.T) {
	rec := recordSpans(t)
	srv, m := start(t)
	id := idOf(upload(t, srv, "alice", "a.png", pngBytes(t, 2, 2, 0)))
	get(t, srv, "/api/v1/avatars/"+id+"?size=1x1")
	m.readErr = errBroken
	get(t, srv, "/api/v1/avatars/"+id)
	spans := ended(t, rec, 3)
	assert.Equal(t, codes.Unset, spans[1].Status().Code, "ответ 400 помечен ошибкой")
	assert.Equal(t, codes.Error, spans[2].Status().Code, "ответ 500 без ошибки")
}
