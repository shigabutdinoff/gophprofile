package storage

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestSpans(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(noop.NewTracerProvider()) })
	status := http.StatusOK
	f := startS3(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
	_ = f.client.Upload(t.Context(), key, []byte("картинка"))
	_, _ = f.client.Download(t.Context(), key, 100)
	_ = f.client.Delete(t.Context(), key)
	_ = f.client.Ping(t.Context())
	status = http.StatusInternalServerError
	_ = f.client.Upload(t.Context(), key, []byte("картинка"))
	var got []string
	for _, s := range rec.Ended() {
		got = append(got, s.Name())
	}
	assert.Equal(t, []string{"S3.PutObject", "S3.GetObject", "S3.DeleteObject", "S3.PutObject"}, got, "проверка Ping попала в трейсы")
	assert.Equal(t, codes.Error, rec.Ended()[3].Status().Code, "сбой хранилища без ошибки в спане")
}
