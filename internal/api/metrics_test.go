package api_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/shigabutdinoff/gophprofile/internal/api"
	"github.com/shigabutdinoff/gophprofile/internal/avatar"
)

type broken struct{ files }

func (broken) Upload(context.Context, string, []byte) error { return errBroken }

func meter(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(provider) })
	return reader
}

type uploads struct {
	total  map[string]int64
	took   map[string]uint64
	bounds []float64
}

func collect(t *testing.T, reader *sdkmetric.ManualReader) uploads {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	got := uploads{total: map[string]int64{}, took: map[string]uint64{}}
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			switch data := m.Data.(type) {
			case metricdata.Sum[int64]:
				if m.Name == "avatars.uploads" {
					for _, p := range data.DataPoints {
						status, _ := p.Attributes.Value("status")
						got.total[status.AsString()] = p.Value
					}
				}
			case metricdata.Histogram[float64]:
				if m.Name == "avatars.upload.duration" {
					assert.Equal(t, "s", m.Unit)
					for _, p := range data.DataPoints {
						status, _ := p.Attributes.Value("status")
						got.took[status.AsString()] = p.Count
						got.bounds = p.Bounds
					}
				}
			}
		}
	}
	return got
}

func TestUploadMetrics(t *testing.T) {
	reader := meter(t)
	srv, m := start(t)
	resp, raw := upload(t, srv, "alice", "me.png", pngBytes(t, 4, 4, 0))
	require.Equal(t, http.StatusCreated, resp.StatusCode())
	got := collect(t, reader)
	assert.Equal(t, map[string]int64{"success": 1}, got.total)
	assert.Equal(t, map[string]uint64{"success": 1}, got.took)
	assert.Equal(t, []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}, got.bounds)
	get(t, srv, "/api/v1/avatars/"+idOf(resp, raw)+"/metadata")
	assert.Equal(t, map[string]int64{"success": 1}, collect(t, reader).total, "чтение попало в загрузки")
	upload(t, srv, "alice", "doc.pdf", []byte("%PDF-1.4"))
	send(t, resty.New().R().SetHeader("X-User-ID", "alice"), http.MethodPost, srv.URL+"/api/v1/avatars")
	got = collect(t, reader)
	assert.Equal(t, map[string]int64{"success": 1, "rejected": 2}, got.total)
	m.pubErr = errBroken
	resp, _ = upload(t, srv, "alice", "me.png", pngBytes(t, 4, 4, 0))
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode())
	assert.Equal(t, int64(1), collect(t, reader).total["error"], "брокер не принял событие")
}

func TestUploadMetricsStorageError(t *testing.T) {
	reader := meter(t)
	m := &memory{objects: map[string][]byte{}, gone: map[string]bool{}}
	srv := httptest.NewServer(api.New(avatar.New(m, broken{files{m}}, m, avatar.MaxSize, []string{"image/jpeg", "image/png", "image/webp"}), http.NotFoundHandler(), avatar.MaxSize, 0))
	t.Cleanup(srv.Close)
	req := resty.New().R().SetHeader("X-User-ID", "alice").SetFileReader("image", "me.png", bytes.NewReader(pngBytes(t, 4, 4, 0)))
	resp, _ := send(t, req, http.MethodPost, srv.URL+"/web/upload")
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode())
	got := collect(t, reader)
	assert.Equal(t, map[string]int64{"error": 1}, got.total)
	assert.Equal(t, map[string]uint64{"error": 1}, got.took)
}

func TestUploadMetricsClientGone(t *testing.T) {
	reader := meter(t)
	m := &memory{objects: map[string][]byte{}, gone: map[string]bool{}}
	h := api.New(avatar.New(m, broken{files{m}}, m, avatar.MaxSize, []string{"image/jpeg", "image/png", "image/webp"}), http.NotFoundHandler(), avatar.MaxSize, 0)
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("file", "me.png")
	_, _ = part.Write(pngBytes(t, 4, 4, 0))
	require.NoError(t, form.Close())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/avatars", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("X-User-ID", "alice")
	h.ServeHTTP(httptest.NewRecorder(), req)
	assert.Equal(t, map[string]int64{"rejected": 1}, collect(t, reader).total, "обрыв клиента посчитан сбоем")
}

func TestHTTPMetricsIgnoreHost(t *testing.T) {
	reader := meter(t)
	srv, _ := start(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "evil.example:1234"
	srv.Config.Handler.ServeHTTP(httptest.NewRecorder(), req)
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	seen := 0
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if data, ok := m.Data.(metricdata.Histogram[float64]); ok && m.Name == "http.server.request.duration" {
				for _, p := range data.DataPoints {
					host, _ := p.Attributes.Value("server.address")
					port, _ := p.Attributes.Value("server.port")
					assert.Equal(t, "gophprofile", host.AsString())
					assert.Equal(t, int64(8080), port.AsInt64())
					seen++
				}
			}
		}
	}
	assert.Equal(t, 1, seen, "метрика запроса не записана")
}
