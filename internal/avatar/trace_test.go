package avatar_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/shigabutdinoff/gophprofile/internal/avatar"
)

func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(noop.NewTracerProvider()) })
	return rec
}

func TestUploadSpan(t *testing.T) {
	rec := recordSpans(t)
	svc, _, _ := newService()
	_, err := svc.Upload(context.Background(), "alice", "me.png", pngBytes(t, 2, 2))
	require.NoError(t, err)
	require.Len(t, rec.Ended(), 1)
	span := rec.Ended()[0]
	assert.Equal(t, "upload_avatar", span.Name())
	assert.Equal(t, codes.Unset, span.Status().Code)
	assert.ElementsMatch(t, []attribute.KeyValue{attribute.String("user_id", "alice"), attribute.String("file_name", "me.png"),
		attribute.Int64("file_size", int64(len(pngBytes(t, 2, 2))))}, span.Attributes())
	_, err = svc.Upload(context.Background(), "alice", "me.png", []byte("не картинка"))
	assert.ErrorIs(t, err, avatar.ErrInvalidFormat)
	assert.Len(t, rec.Ended(), 1, "отказ валидации дал спан")
}

func TestUploadSpanErrors(t *testing.T) {
	rec := recordSpans(t)
	svc, _, files, pub := withPublisher()
	files.fail = errBroken
	_, err := svc.Upload(context.Background(), "alice", "me.png", pngBytes(t, 1, 1))
	require.ErrorIs(t, err, errBroken)
	files.fail = nil
	pub.fail = errBroken
	_, err = svc.Upload(context.Background(), "alice", "me.png", pngBytes(t, 1, 1))
	require.ErrorIs(t, err, avatar.ErrUnavailable)
	for i, span := range rec.Ended() {
		assert.Equal(t, codes.Error, span.Status().Code, "сбой %d без ошибки в спане", i)
	}
}

func TestThumbnailsSpan(t *testing.T) {
	svc, _, _, a := uploaded(t, pngBytes(t, 2, 2))
	rec := recordSpans(t)
	require.NoError(t, svc.HandleUploadEvent(context.Background(), event(a)))
	require.Len(t, rec.Ended(), 1)
	assert.Equal(t, "thumbnails", rec.Ended()[0].Name())
	assert.Equal(t, codes.Unset, rec.Ended()[0].Status().Code)
	svc, repo, files, a := uploaded(t, pngBytes(t, 2, 2))
	files.onUpload = func(string) { repo.rows[0].Status = avatar.StatusCompleted }
	require.ErrorIs(t, svc.HandleUploadEvent(context.Background(), event(a)), avatar.ErrDone)
	last := rec.Ended()[len(rec.Ended())-1]
	assert.Equal(t, "thumbnails", last.Name())
	assert.Equal(t, codes.Unset, last.Status().Code, "повтор события помечен ошибкой")
}

func TestUploadRecord(t *testing.T) {
	rec := recordSpans(t)
	t.Setenv("OTEL_SERVICE_NAME", "gophprofile-server")
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	svc, _, _ := newService()
	data := pngBytes(t, 2, 2)
	_, err := svc.Upload(context.Background(), "alice", "me.png", data)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "INFO", got["level"])
	assert.Equal(t, "uploading avatar", got["msg"])
	assert.Equal(t, "gophprofile-server", got["service"])
	assert.Equal(t, rec.Ended()[0].SpanContext().TraceID().String(), got["trace_id"], "запись вне trace загрузки")
	assert.Equal(t, "alice", got["user_id"])
	assert.InDelta(t, len(data), got["file_size"], 0)
	assert.Equal(t, "image/png", got["mime_type"])
	buf.Reset()
	_, err = svc.Upload(context.Background(), "alice", "me.png", []byte("не картинка"))
	require.ErrorIs(t, err, avatar.ErrInvalidFormat)
	assert.Empty(t, buf.String(), "отказ валидации дал запись")
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	_, err = svc.Upload(context.Background(), "alice", "me.png", data)
	require.NoError(t, err)
	assert.NotContains(t, buf.String(), "uploading avatar", "запись ниже порога")
}
