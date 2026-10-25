package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophprofile/internal/api"
	"github.com/shigabutdinoff/gophprofile/internal/avatar"
)

var (
	errBroken = errors.New("сбой хранилища")
	client    = resty.New()
)

type memory struct {
	rows    []avatar.Avatar
	gone    map[string]bool
	objects map[string][]byte
	readErr error
	pubErr  error
}

func (m *memory) UpdateProcessingStatus(context.Context, string, avatar.Status, map[string]string) error {
	return avatar.ErrDone
}

func (m *memory) Delete(_ context.Context, id string) error {
	m.rows = slices.DeleteFunc(m.rows, func(a avatar.Avatar) bool { return a.ID == id })
	return nil
}

func (m *memory) MarkDeleted(_ context.Context, id string) error {
	if m.gone[id] {
		return avatar.ErrNotFound
	}
	m.gone[id] = true
	return nil
}

func (m *memory) Publish(context.Context, string, string, any) error { return m.pubErr }

func (m *memory) Create(_ context.Context, a *avatar.Avatar) error {
	a.CreatedAt = time.Date(2026, 10, 23, 15, len(m.rows), 0, 123456789, time.FixedZone("MSK", 3*60*60))
	a.UpdatedAt = a.CreatedAt
	m.rows = append([]avatar.Avatar{*a}, m.rows...)
	return nil
}

func (m *memory) GetAvatar(_ context.Context, id string) (avatar.Avatar, error) {
	for _, a := range m.rows {
		if a.ID == id && !m.gone[id] {
			return a, nil
		}
	}
	return avatar.Avatar{}, avatar.ErrNotFound
}

func (m *memory) List(_ context.Context, userID string, limit int) ([]avatar.Avatar, error) {
	list := []avatar.Avatar{}
	for _, a := range m.rows {
		if a.UserID == userID && !m.gone[a.ID] && (limit == 0 || len(list) < limit) {
			list = append(list, a)
		}
	}
	return list, nil
}

func (m *memory) Upload(_ context.Context, key string, data []byte) error {
	m.objects[key] = data
	return nil
}

type files struct{ *memory }

func (f files) Delete(_ context.Context, key string) error {
	delete(f.objects, key)
	return nil
}

func (f files) Download(_ context.Context, key string, _ int64) ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.objects[key], nil
}

func etag(data []byte) string {
	return fmt.Sprintf(`"%x"`, sha256.Sum256(data))
}

func start(t *testing.T) (*httptest.Server, *memory) {
	t.Helper()
	m := &memory{objects: map[string][]byte{}, gone: map[string]bool{}}
	srv := httptest.NewServer(api.New(avatar.New(m, files{m}, m), http.NotFoundHandler()))
	t.Cleanup(srv.Close)
	return srv, m
}

func pngBytes(t *testing.T, w, h int, pad int) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))), "PNG не собран")
	return append(buf.Bytes(), make([]byte, pad)...)
}

func upload(t *testing.T, srv *httptest.Server, user, name string, data []byte) (*http.Response, []byte) {
	t.Helper()
	req := client.R().SetFileReader("file", name, bytes.NewReader(data))
	if user != "" {
		req.SetHeader("X-User-ID", user)
	}
	return send(t, req, http.MethodPost, srv.URL+"/api/v1/avatars")
}

func send(t *testing.T, req *resty.Request, method, url string) (*http.Response, []byte) {
	t.Helper()
	resp, err := req.Execute(method, url)
	require.NoError(t, err, "сервер не ответил")
	return resp.RawResponse, resp.Body()
}

func decode(body []byte) map[string]any {
	var got map[string]any
	_ = json.Unmarshal(body, &got)
	return got
}

func idOf(_ *http.Response, body []byte) string {
	id, _ := decode(body)["id"].(string)
	return id
}

func get(t *testing.T, srv *httptest.Server, path string, header ...string) (*http.Response, []byte) {
	t.Helper()
	req := client.R()
	for i := 0; i+1 < len(header); i += 2 {
		req.SetHeader(header[i], header[i+1])
	}
	return send(t, req, http.MethodGet, srv.URL+path)
}

func expect(t *testing.T, resp *http.Response, body []byte, status int, want map[string]any) {
	t.Helper()
	got := decode(body)
	assert.Equal(t, status, resp.StatusCode, "тело %v", got)
	assert.Subset(t, got, want)
}

func TestUpload(t *testing.T) {
	srv, _ := start(t)
	data := pngBytes(t, 4, 4, 2<<20)
	resp, raw := upload(t, srv, "alice", "me.png", data)
	got := decode(raw)
	id, _ := got["id"].(string)
	require.NotEmpty(t, id, "в ответе нет id")
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, `{"id":"`+id+`","user_id":"alice","url":"/api/v1/avatars/`+id+`","status":"processing",`+
		`"created_at":"2026-10-23T12:00:00Z"}`+"\n", string(raw), "ожидались поля и порядок из ТЗ")
	file, body := get(t, srv, got["url"].(string))
	assert.Equal(t, http.StatusOK, file.StatusCode)
	assert.Equal(t, "image/png", file.Header.Get("Content-Type"))
	assert.True(t, bytes.Equal(data, body), "по ссылке ожидался тот же PNG")
}

func TestUploadFromPage(t *testing.T) {
	srv, _ := start(t)
	resp, body := get(t, srv, "/")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.Contains(t, string(body), "formData.append('image'", "ожидалась страница из шаблона")
	req := client.R().SetHeader("X-User-ID", "alice").SetFileReader("image", "me.png", bytes.NewReader(pngBytes(t, 4, 4, 0)))
	resp, raw := send(t, req, http.MethodPost, srv.URL+"/api/v1/avatars")
	assert.Equal(t, http.StatusCreated, resp.StatusCode, "загрузка со страницы не принята, ответ %s", raw)
}

func TestUploadErrors(t *testing.T) {
	srv, m := start(t)
	resp, got := upload(t, srv, "alice", "big.png", make([]byte, 12<<20))
	expect(t, resp, got, http.StatusRequestEntityTooLarge, map[string]any{"error": "File too large", "max_size": float64(avatar.MaxSize)})
	resp, got = upload(t, srv, "alice", "big.png", pngBytes(t, 1, 1, avatar.MaxSize))
	expect(t, resp, got, http.StatusRequestEntityTooLarge, map[string]any{"error": "File too large"})
	resp, raw := upload(t, srv, "alice", "photo.jpg", []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n"))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, `{"error":"Invalid file format","details":"Supported formats: jpeg, png, webp"}`+"\n", string(raw))
	resp, got = upload(t, srv, "", "me.png", pngBytes(t, 1, 1, 0))
	expect(t, resp, got, http.StatusBadRequest, map[string]any{"error": "неверный идентификатор пользователя"})
	resp, body := send(t, client.R().SetHeader("X-User-ID", "alice").SetBody("{}"), http.MethodPost, srv.URL+"/api/v1/avatars")
	expect(t, resp, body, http.StatusBadRequest, map[string]any{"error": "неверный запрос: не передан файл"})
	req := client.R().SetHeader("X-User-ID", "alice").SetFormData(map[string]string{"pad": strings.Repeat("x", avatar.MaxSize+1<<20)}).
		SetFileReader("file", "me.png", bytes.NewReader(pngBytes(t, 1, 1, 0)))
	resp, body = send(t, req, http.MethodPost, srv.URL+"/api/v1/avatars")
	expect(t, resp, body, http.StatusRequestEntityTooLarge, map[string]any{"error": "File too large"})
	assert.Empty(t, m.rows, "после отказов создана запись")
	assert.Empty(t, m.objects, "после отказов записан файл")
	_, list := get(t, srv, "/api/v1/users/alice/avatars")
	assert.Equal(t, "[]\n", string(list))
}

func TestMetadataAndList(t *testing.T) {
	srv, _ := start(t)
	first := idOf(upload(t, srv, "alice", "old.png", pngBytes(t, 1, 1, 0)))
	me := idOf(upload(t, srv, "alice", "me.png", pngBytes(t, 1920, 1080, 0)))
	id := me
	resp, body := get(t, srv, "/api/v1/avatars/"+id+"/metadata")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, fmt.Sprintf(`{"id":"%s","user_id":"alice","file_name":"me.png","mime_type":"image/png","size":%d,`+
		`"dimensions":{"width":1920,"height":1080},"thumbnails":[],"created_at":"2026-10-23T12:01:00Z",`+
		`"updated_at":"2026-10-23T12:01:00Z"}`+"\n", id, len(pngBytes(t, 1920, 1080, 0))), string(body), "ожидались поля и порядок из ТЗ")
	_, body = get(t, srv, "/api/v1/users/alice/avatars")
	var list []avatar.Avatar
	require.NoError(t, json.Unmarshal(body, &list))
	require.Len(t, list, 2)
	assert.Equal(t, []string{me, first}, []string{list[0].ID, list[1].ID}, "ожидались me.png и old.png")
	_, escaped := get(t, srv, "/api/v1/users/%61lice/avatars")
	assert.Equal(t, body, escaped, "закодированный идентификатор не раскодирован")
}

func TestCurrentAndCache(t *testing.T) {
	srv, _ := start(t)
	resp, body := get(t, srv, "/api/v1/users/alice/avatar")
	expect(t, resp, body, http.StatusNotFound, map[string]any{"error": "Avatar not found"})
	a := pngBytes(t, 1, 1, 0)
	b := pngBytes(t, 2, 2, 0)
	first := idOf(upload(t, srv, "alice", "a.png", a))
	upload(t, srv, "alice", "b.png", b)
	cur, body := get(t, srv, "/api/v1/users/alice/avatar")
	assert.Equal(t, b, body, "текущей ожидалась B")
	assert.Equal(t, "no-cache", cur.Header.Get("Cache-Control"))
	assert.Equal(t, etag(b), cur.Header.Get("ETag"))
	byID, _ := get(t, srv, "/api/v1/avatars/"+first+"?size=original")
	assert.Equal(t, "max-age=86400", byID.Header.Get("Cache-Control"), "ответ по id")
	again, body := get(t, srv, "/api/v1/avatars/"+first, "If-None-Match", byID.Header.Get("ETag"))
	assert.Equal(t, http.StatusNotModified, again.StatusCode, "повтор с ETag")
	assert.Empty(t, body, "у ответа 304 есть тело")
	again, _ = get(t, srv, "/api/v1/users/alice/avatar", "If-None-Match", etag(b))
	assert.Equal(t, http.StatusNotModified, again.StatusCode, "повтор текущей с ETag")
	sized, body := get(t, srv, "/api/v1/avatars/"+first+"?size=100x100")
	cfg, format, err := image.DecodeConfig(bytes.NewReader(body))
	require.NoError(t, err, "до обработки ожидалась миниатюра")
	assert.Equal(t, []any{"jpeg", 100, 100}, []any{format, cfg.Width, cfg.Height})
	assert.Equal(t, "image/jpeg", sized.Header.Get("Content-Type"))
	assert.Equal(t, "max-age=86400", sized.Header.Get("Cache-Control"))
}

func TestFormat(t *testing.T) {
	srv, m := start(t)
	data := pngBytes(t, 3, 2, 0)
	id := idOf(upload(t, srv, "alice", "a.png", data))
	for _, format := range []string{"jpeg", "webp"} {
		resp, body := get(t, srv, "/api/v1/avatars/"+id+"?format="+format)
		require.Equal(t, http.StatusOK, resp.StatusCode, format)
		assert.Equal(t, "image/"+format, resp.Header.Get("Content-Type"))
		assert.Equal(t, etag(body), resp.Header.Get("ETag"))
		assert.Equal(t, "max-age=86400", resp.Header.Get("Cache-Control"))
		_, got, err := image.DecodeConfig(bytes.NewReader(body))
		require.NoError(t, err, format)
		assert.Equal(t, format, got)
	}
	resp, body := get(t, srv, "/api/v1/users/alice/avatar?format=png")
	assert.Equal(t, data, body, "тот же формат ожидался без перекодирования")
	assert.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))
	m.rows[0].Dimensions = avatar.Dimensions{Width: 10000, Height: 10000}
	resp, body = get(t, srv, "/api/v1/avatars/"+id+"?format=jpeg")
	expect(t, resp, body, http.StatusBadRequest, map[string]any{"error": avatar.ErrTooManyPixels.Error()})
}

func TestBadRequests(t *testing.T) {
	srv, m := start(t)
	id := idOf(upload(t, srv, "alice", "a.png", pngBytes(t, 1, 1, 0)))
	for _, path := range []string{"/api/v1/avatars/" + id + "?size=50x50", "/api/v1/avatars/" + id + "?format=gif", "/api/v1/avatars/" + id + "?format=",
		"/api/v1/users/alice/avatar?size=50x50"} {
		resp, body := get(t, srv, path)
		expect(t, resp, body, http.StatusBadRequest, map[string]any{"error": "неверный запрос: неверный размер или формат"})
	}
	for _, path := range []string{"/api/v1/avatars/abc", "/api/v1/avatars/abc/metadata", "/api/v1/avatars/0199b5e4-7c3a-7d21-9f4e-2a6c8b1d3e57"} {
		resp, body := get(t, srv, path)
		expect(t, resp, body, http.StatusNotFound, map[string]any{"error": "Avatar not found"})
	}
	m.readErr = errBroken
	resp, body := get(t, srv, "/api/v1/avatars/"+id)
	expect(t, resp, body, http.StatusInternalServerError, map[string]any{"error": "внутренняя ошибка"})
}

func TestUnavailableBroker(t *testing.T) {
	srv, m := start(t)
	m.pubErr = errBroken
	resp, got := upload(t, srv, "alice", "me.png", pngBytes(t, 1, 1, 0))
	expect(t, resp, got, http.StatusServiceUnavailable, map[string]any{"error": avatar.ErrUnavailable.Error()})
	assert.Empty(t, m.rows, "запись не откатилась")
	assert.Empty(t, m.objects, "файл не откатился")
}

func TestThumbnails(t *testing.T) {
	srv, m := start(t)
	id := idOf(upload(t, srv, "alice", "me.png", pngBytes(t, 1, 1, 0)))
	_, body := get(t, srv, "/api/v1/avatars/"+id+"/metadata")
	assert.Equal(t, []any{}, decode(body)["thumbnails"], "миниатюры до обработки")
	m.rows[0].Status = avatar.StatusCompleted
	m.rows[0].ThumbnailKeys = map[string]string{"100x100": "thumbnails/" + id + "/100x100.jpg", "300x300": "thumbnails/" + id + "/300x300.jpg"}
	m.objects["thumbnails/"+id+"/100x100.jpg"] = []byte("миниатюра")
	resp, body := get(t, srv, "/api/v1/avatars/"+id+"?size=100x100")
	assert.Equal(t, "миниатюра", string(body))
	assert.Equal(t, "image/jpeg", resp.Header.Get("Content-Type"))
	assert.Equal(t, "max-age=86400", resp.Header.Get("Cache-Control"))
	assert.Equal(t, etag(body), resp.Header.Get("ETag"))
	again, _ := get(t, srv, "/api/v1/avatars/"+id+"?size=100x100", "If-None-Match", resp.Header.Get("ETag"))
	assert.Equal(t, http.StatusNotModified, again.StatusCode)
	_, body = get(t, srv, "/api/v1/avatars/"+id+"/metadata")
	assert.Equal(t, []any{
		map[string]any{"size": "100x100", "url": "/api/v1/avatars/" + id + "?size=100x100"},
		map[string]any{"size": "300x300", "url": "/api/v1/avatars/" + id + "?size=300x300"},
	}, decode(body)["thumbnails"])
}

func TestHealth(t *testing.T) {
	broken := errors.New("секретный адрес 10.0.0.1")
	ok := func(context.Context) error { return nil }
	srv := httptest.NewServer(api.New(nil, api.Health(map[string]func(context.Context) error{
		"postgres": ok, "rabbitmq": ok, "s3": func(context.Context) error { return broken }})))
	t.Cleanup(srv.Close)
	resp, body := get(t, srv, "/health")
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.NotContains(t, string(body), broken.Error(), "текст ошибки в ответе")
	details := decode(body)["details"].(map[string]any)
	assert.Equal(t, "down", details["s3"].(map[string]any)["status"])
	assert.Equal(t, "up", details["postgres"].(map[string]any)["status"])
	srv = httptest.NewServer(api.Health(map[string]func(context.Context) error{"postgres": ok}))
	t.Cleanup(srv.Close)
	resp, _ = get(t, srv, "/")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func remove(t *testing.T, srv *httptest.Server, user, path string) (*http.Response, []byte) {
	t.Helper()
	req := client.R()
	if user != "" {
		req.SetHeader("X-User-ID", user)
	}
	return send(t, req, http.MethodDelete, srv.URL+path)
}

func TestDelete(t *testing.T) {
	srv, m := start(t)
	id := idOf(upload(t, srv, "alice", "a.png", pngBytes(t, 2, 2, 0)))
	resp, body := remove(t, srv, "bob", "/api/v1/avatars/"+id)
	expect(t, resp, body, http.StatusForbidden, map[string]any{"error": "Forbidden", "details": "You can only delete your own avatars"})
	resp, body = remove(t, srv, "", "/api/v1/avatars/"+id)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "без X-User-ID: %s", body)
	resp, body = remove(t, srv, "alice", "/api/v1/avatars/"+id)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode, "удаление: %s", body)
	assert.Empty(t, body, "у ответа 204 есть тело")
	resp, body = get(t, srv, "/api/v1/avatars/"+id)
	expect(t, resp, body, http.StatusNotFound, map[string]any{"error": "Avatar not found"})
	resp, body = remove(t, srv, "alice", "/api/v1/avatars/"+id)
	expect(t, resp, body, http.StatusNotFound, map[string]any{"error": "Avatar not found"})
	assert.True(t, m.gone[id])
}

func TestDeleteCurrent(t *testing.T) {
	srv, _ := start(t)
	first := idOf(upload(t, srv, "a/b", "a.png", pngBytes(t, 2, 2, 0)))
	upload(t, srv, "a/b", "b.png", pngBytes(t, 3, 3, 0))
	resp, body := remove(t, srv, "bob", "/api/v1/users/a%2Fb/avatar")
	expect(t, resp, body, http.StatusForbidden, map[string]any{"error": "Forbidden"})
	resp, body = remove(t, srv, "a/b", "/api/v1/users/a%2Fb/avatar")
	assert.Equal(t, http.StatusNoContent, resp.StatusCode, "удаление текущей: %s", body)
	resp, _ = get(t, srv, "/api/v1/avatars/"+first+"/metadata")
	require.Equal(t, http.StatusOK, resp.StatusCode, "предыдущая аватарка удалена")
	resp, current := get(t, srv, "/api/v1/users/a%2Fb/avatar")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, pngBytes(t, 2, 2, 0), current, "текущей не стала предыдущая")
}

func TestDeleteUnavailable(t *testing.T) {
	srv, m := start(t)
	id := idOf(upload(t, srv, "alice", "a.png", pngBytes(t, 2, 2, 0)))
	m.pubErr = errBroken
	resp, body := remove(t, srv, "alice", "/api/v1/avatars/"+id)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, "тело %s", body)
	resp, _ = get(t, srv, "/api/v1/avatars/"+id)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "аватарка не читается после отката")
}

func TestRateLimit(t *testing.T) {
	srv, _ := start(t)
	for range 100 {
		resp, _ := get(t, srv, "/health")
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	}
	resp, _ := get(t, srv, "/health")
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode, "101-й запрос с одного IP")
}
