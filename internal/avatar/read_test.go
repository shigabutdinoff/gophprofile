package avatar_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophprofile/internal/avatar"
)

func uploaded(t *testing.T, data []byte) (*avatar.Service, *fakeRepo, *fakeFiles, avatar.Avatar) {
	t.Helper()
	svc, repo, files := newService()
	a, err := svc.Upload(context.Background(), "alice", "me.png", data)
	require.NoError(t, err, "загрузка не прошла")
	return svc, repo, files, a
}

func TestReadSize(t *testing.T) {
	svc, _, files, a := uploaded(t, pngBytes(t, 2, 2))
	early, mime, err := svc.Read(context.Background(), a, "100x100")
	require.NoError(t, err)
	assert.Equal(t, "image/jpeg", mime)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(early))
	require.NoError(t, err, "до обработки ожидалась миниатюра")
	assert.Equal(t, []any{"jpeg", 100, 100}, []any{format, cfg.Width, cfg.Height})
	data, mime, err := svc.Read(context.Background(), a, avatar.Original)
	require.NoError(t, err)
	assert.Equal(t, files.objects[a.S3Key], data)
	assert.Equal(t, "image/png", mime)
	huge := a
	huge.Dimensions = avatar.Dimensions{Width: 30000, Height: 30000}
	_, _, err = svc.Read(context.Background(), huge, "100x100")
	assert.ErrorIs(t, err, avatar.ErrTooManyPixels)
}

func TestTransparentOnWhite(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 10, 10))))
	svc, _, _, a := uploaded(t, buf.Bytes())
	data, _, err := svc.Read(context.Background(), a, "100x100")
	require.NoError(t, err)
	img, _, err := image.Decode(bytes.NewReader(data))
	require.NoError(t, err)
	r, g, b, _ := img.At(50, 50).RGBA()
	assert.True(t, r > 0xf000 && g > 0xf000 && b > 0xf000, "прозрачное стало не белым")
}

func TestGet(t *testing.T) {
	svc, repo, _ := newService()
	_, err := svc.Get(context.Background(), "abc")
	assert.ErrorIs(t, err, avatar.ErrNotFound)
	assert.Zero(t, repo.getting, "неверный id дошёл до базы")
	a, err := svc.Upload(context.Background(), "alice", "a", pngBytes(t, 1, 1))
	require.NoError(t, err, "загрузка не прошла")
	got, err := svc.Get(context.Background(), strings.ToUpper(a.ID))
	require.NoError(t, err)
	assert.Equal(t, a.ID, got.ID)
}

func TestCurrentAndRead(t *testing.T) {
	svc, repo, _ := newService()
	_, err := svc.Current(context.Background(), "alice")
	assert.ErrorIs(t, err, avatar.ErrNotFound)
	second := pngBytes(t, 2, 2)
	a, err := svc.Upload(context.Background(), "alice", "a.png", pngBytes(t, 1, 1))
	require.NoError(t, err, "загрузка A не прошла")
	b, err := svc.Upload(context.Background(), "alice", "b.png", second)
	require.NoError(t, err, "загрузка B не прошла")
	cur, err := svc.Current(context.Background(), "alice")
	require.NoError(t, err)
	assert.Equal(t, b.ID, cur.ID, "текущей ожидалась B")
	data, mime, err := svc.Read(context.Background(), cur, avatar.Original)
	require.NoError(t, err)
	assert.Equal(t, second, data, "ожидались байты B")
	assert.Equal(t, "image/png", mime)
	list, err := svc.List(context.Background(), "alice")
	require.NoError(t, err)
	assert.Equal(t, []avatar.Avatar{b, a}, list, "ожидались B и A")
	repo.fail = errBroken
	_, err = svc.Current(context.Background(), "alice")
	assert.ErrorIs(t, err, errBroken, "ожидался сбой базы")
}

func TestInvalidUTF8(t *testing.T) {
	svc, repo, _ := newService()
	a, err := svc.Upload(context.Background(), "alice", "a\x00b\xff.png", pngBytes(t, 1, 1))
	require.NoError(t, err)
	assert.Equal(t, "ab.png", a.FileName)
	a, err = svc.Upload(context.Background(), "alice", strings.Repeat("я", 300), pngBytes(t, 1, 1))
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("я", 255), a.FileName, "имя не обрезано до 255 символов")
	repo.fail = errBroken
	for _, id := range []string{"\xff", "a\x00"} {
		list, err := svc.List(context.Background(), id)
		require.NoError(t, err, "%q дошёл до базы", id)
		assert.Equal(t, []avatar.Avatar{}, list, "%q", id)
		_, err = svc.Current(context.Background(), id)
		assert.ErrorIs(t, err, avatar.ErrNotFound, "%q", id)
	}
}
