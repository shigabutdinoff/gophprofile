package storage

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	key  = "originals/0199b5e4-7c3a-7d21-9f4e-2a6c8b1d3e57"
	path = "/avatars/" + key
)

type fakeS3 struct {
	*httptest.Server
	requests atomic.Int32
	client   *Client
}

func startS3(t *testing.T, reply http.HandlerFunc) *fakeS3 {
	t.Helper()
	f := &fakeS3{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		reply(w, r)
	}))
	t.Cleanup(f.Close)
	web := f.Client()
	web.Timeout = 300 * time.Millisecond
	f.client = New(web, f.URL+"/", "minio", "секрет")
	return f
}

func TestPut(t *testing.T) {
	var method, target, auth, body string
	f := startS3(t, func(_ http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		method, target, body = r.Method, r.URL.Path, string(data)
		auth = r.Header.Get("Authorization")
	})
	require.NoError(t, f.client.Upload(t.Context(), key, []byte("картинка")), "объект не записан")
	assert.Equal(t, []string{http.MethodPut, path, "картинка"}, []string{method, target, body})
	assert.Regexp(t, `Credential=minio/\d{8}/us-east-1/s3/aws4_request`, auth, "в подписи нет ключа доступа и региона")
	assert.Equal(t, int32(1), f.requests.Load(), "ожидался один запрос записи")
}

func TestGet(t *testing.T) {
	var method, target string
	f := startS3(t, func(w http.ResponseWriter, r *http.Request) {
		method, target = r.Method, r.URL.Path
		_, _ = w.Write([]byte("картинка"))
	})
	data, err := f.client.Download(t.Context(), key, 100)
	require.NoError(t, err)
	assert.Equal(t, "картинка", string(data))
	assert.Equal(t, []string{http.MethodGet, path}, []string{method, target})
	_, err = f.client.Download(t.Context(), key, 5)
	assert.Error(t, err, "объект больше допустимого размера принят")
}

func TestSingleAttempt(t *testing.T) {
	replies := map[string]http.HandlerFunc{
		"сервер держит ответ": func(_ http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			<-r.Context().Done()
		},
		"ответ 503": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) },
	}
	for name, reply := range replies {
		f := startS3(t, reply)
		_, err := f.client.Download(t.Context(), key, 100)
		assert.Error(t, err, "%s дал чтение без сбоя", name)
		assert.Error(t, f.client.Upload(t.Context(), key, []byte("картинка")), "запись прошла: %s", name)
		assert.Equal(t, int32(2), f.requests.Load(), "%s, ожидалось два запроса", name)
	}
}
