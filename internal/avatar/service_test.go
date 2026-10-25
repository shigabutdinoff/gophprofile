package avatar_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophprofile/internal/avatar"
)

var errBroken = errors.New("сбой хранилища")

const webp1x1 = "RIFF\x1a\x00\x00\x00WEBPVP8L\x0d\x00\x00\x00\x2f\x00\x00\x00\x10\x07\x10\x11\x11\x88\x88\xfe\x07\x00"

type fakeRepo struct {
	rows    []avatar.Avatar
	fail    error
	getting int
}

func (r *fakeRepo) UpdateProcessingStatus(_ context.Context, id string, status avatar.Status, keys map[string]string) error {
	if r.fail != nil {
		return r.fail
	}
	for i, a := range r.rows {
		if a.ID == id && a.Status == avatar.StatusProcessing {
			r.rows[i].Status, r.rows[i].ThumbnailKeys = status, keys
			return nil
		}
	}
	return avatar.ErrDone
}

func (r *fakeRepo) Delete(_ context.Context, id string) error {
	r.rows = slices.DeleteFunc(r.rows, func(a avatar.Avatar) bool { return a.ID == id })
	return nil
}

func (r *fakeRepo) Create(_ context.Context, a *avatar.Avatar) error {
	if r.fail != nil {
		return r.fail
	}
	a.CreatedAt = time.Date(2026, 10, 23, 12, len(r.rows), 0, 0, time.UTC)
	a.UpdatedAt = a.CreatedAt
	r.rows = append([]avatar.Avatar{*a}, r.rows...)
	return nil
}

func (r *fakeRepo) GetAvatar(_ context.Context, id string) (avatar.Avatar, error) {
	r.getting++
	if r.fail != nil {
		return avatar.Avatar{}, r.fail
	}
	for _, a := range r.rows {
		if a.ID == id {
			return a, nil
		}
	}
	return avatar.Avatar{}, avatar.ErrNotFound
}

func (r *fakeRepo) List(_ context.Context, userID string, limit int) ([]avatar.Avatar, error) {
	if r.fail != nil {
		return nil, r.fail
	}
	list := []avatar.Avatar{}
	for _, a := range r.rows {
		if a.UserID == userID && (limit == 0 || len(list) < limit) {
			list = append(list, a)
		}
	}
	return list, nil
}

type fakeFiles struct {
	objects map[string][]byte
	fail    error
}

func (f *fakeFiles) Upload(_ context.Context, key string, data []byte) error {
	if f.fail != nil {
		return f.fail
	}
	f.objects[key] = data
	return nil
}

func (f *fakeFiles) Download(_ context.Context, key string, _ int64) ([]byte, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	return f.objects[key], nil
}

func (f *fakeFiles) Delete(_ context.Context, key string) error {
	delete(f.objects, key)
	return nil
}

type fakePub struct {
	routes []string
	events []any
	fail   error
}

func (p *fakePub) Publish(_ context.Context, exchange, key string, event any) error {
	p.routes = append(p.routes, exchange+" "+key)
	p.events = append(p.events, event)
	return p.fail
}

func newService() (*avatar.Service, *fakeRepo, *fakeFiles) {
	svc, repo, files, _ := withPublisher()
	return svc, repo, files
}

func withPublisher() (*avatar.Service, *fakeRepo, *fakeFiles, *fakePub) {
	repo := &fakeRepo{}
	files := &fakeFiles{objects: map[string][]byte{}}
	pub := &fakePub{}
	return avatar.New(repo, files, pub), repo, files, pub
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))), "PNG не собран")
	return buf.Bytes()
}

func jpegBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 4, 3)), nil), "JPEG не собран")
	return buf.Bytes()
}

func TestUploadPNG(t *testing.T) {
	svc, repo, files := newService()
	data := pngBytes(t, 1920, 1080)
	a, err := svc.Upload(context.Background(), "alice", "me.png", data)
	require.NoError(t, err, "загрузка не прошла")
	assert.Equal(t, avatar.Avatar{ID: a.ID, UserID: "alice", FileName: "me.png", MIMEType: "image/png", Size: int64(len(data)),
		Dimensions: avatar.Dimensions{Width: 1920, Height: 1080}, S3Key: "originals/" + a.ID, Status: avatar.StatusProcessing, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}, a)
	assert.Equal(t, data, files.objects["originals/"+a.ID], "файл не лежит под ключом оригинала")
	assert.Equal(t, []avatar.Avatar{a}, repo.rows, "ожидалась одна запись")
	assert.False(t, a.CreatedAt.IsZero(), "у записи нет даты")
}

func TestUploadFormats(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		mime string
	}{
		{"jpeg", jpegBytes(t), "image/jpeg"},
		{"webp", []byte(webp1x1), "image/webp"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, _, _ := newService()
			a, err := svc.Upload(context.Background(), "alice", "a", c.data)
			require.NoError(t, err, "загрузка не прошла")
			assert.Equal(t, c.mime, a.MIMEType)
		})
	}
}

func webpCanvas(canvas, frame int) []byte {
	chunk := func(id string, data []byte) []byte {
		return append(binary.LittleEndian.AppendUint32([]byte(id), uint32(len(data))), data...)
	}
	c := byte(canvas - 1)
	f := uint32(frame-1) | uint32(frame-1)<<14
	body := append([]byte("WEBP"), chunk("VP8X", []byte{0, 0, 0, 0, c, 0, 0, c, 0, 0})...)
	body = append(body, chunk("VP8L", append([]byte{0x2f}, binary.LittleEndian.AppendUint32(nil, f)...))...)
	return append(binary.LittleEndian.AppendUint32([]byte("RIFF"), uint32(len(body))), body...)
}

func TestUploadWebPCanvas(t *testing.T) {
	svc, _, _ := newService()
	_, err := svc.Upload(context.Background(), "alice", "a.webp", webpCanvas(2, 2))
	assert.NoError(t, err, "холст равный кадру отклонён")
}

func TestUploadRejects(t *testing.T) {
	cases := []struct {
		name string
		user string
		data []byte
		want error
	}{
		{"pdf", "alice", []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n1 0 obj\n"), avatar.ErrInvalidFormat},
		{"gif", "alice", []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"), avatar.ErrInvalidFormat},
		{"webp_canvas", "alice", webpCanvas(1, 200), avatar.ErrInvalidFormat},
		{"too_large", "alice", make([]byte, avatar.MaxSize+1), avatar.ErrTooLarge},
		{"no_user", "", []byte("x"), avatar.ErrUserID},
		{"nul_user", "a\x00", []byte("x"), avatar.ErrUserID},
		{"utf8_user", "Jos\xe9", []byte("x"), avatar.ErrUserID},
		{"long_user", strings.Repeat("я", 256), []byte("x"), avatar.ErrUserID},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, repo, files := newService()
			_, err := svc.Upload(context.Background(), c.user, "photo.jpg", c.data)
			assert.ErrorIs(t, err, c.want)
			assert.Empty(t, repo.rows, "после отказа создана запись")
			assert.Empty(t, files.objects, "после отказа записан файл")
		})
	}
}

func TestUploadUserLimit(t *testing.T) {
	svc, _, _ := newService()
	_, err := svc.Upload(context.Background(), strings.Repeat("я", 255), "a", pngBytes(t, 1, 1))
	assert.NoError(t, err, "идентификатор из 255 символов отклонён")
}

func TestUploadStorageFailures(t *testing.T) {
	svc, repo, files := newService()
	files.fail = errBroken
	_, err := svc.Upload(context.Background(), "alice", "a", pngBytes(t, 1, 1))
	assert.ErrorIs(t, err, errBroken, "ожидался сбой хранилища")
	assert.Empty(t, repo.rows, "запись создана без файла")
	files.fail = nil
	repo.fail = errBroken
	_, err = svc.Upload(context.Background(), "alice", "a", pngBytes(t, 1, 1))
	assert.ErrorIs(t, err, errBroken, "ожидался сбой базы")
	assert.Len(t, files.objects, 1, "файл удалён, хотя запись могла сохраниться")
}

func TestValidSize(t *testing.T) {
	for size, want := range map[string]bool{"original": true, "100x100": true, "300x300": true, "50x50": false, "": false} {
		assert.Equal(t, want, avatar.ValidSize(size), "размер %q", size)
	}
}
