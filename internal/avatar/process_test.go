package avatar_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophprofile/internal/avatar"
)

func striped(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		c := color.RGBA{G: 255, A: 255}
		if x < (w-h)/2 || x >= (w+h)/2 {
			c = color.RGBA{R: 255, A: 255}
		}
		for y := range h {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func event(a avatar.Avatar) avatar.AvatarUploadEvent {
	return avatar.AvatarUploadEvent{AvatarID: a.ID, UserID: a.UserID, S3Key: a.S3Key}
}

func thumb(t *testing.T, files *fakeFiles, id, size string) (image.Image, string) {
	t.Helper()
	img, format, err := image.Decode(bytes.NewReader(files.objects["thumbnails/"+id+"/"+size+".jpg"]))
	require.NoError(t, err, "миниатюра %s не читается", size)
	return img, format
}

func TestProcessCentered(t *testing.T) {
	svc, repo, files, a := uploaded(t, striped(t, 1920, 1080))
	require.NoError(t, svc.HandleUploadEvent(context.Background(), event(a)))
	for _, side := range []int{100, 300} {
		size := map[int]string{100: "100x100", 300: "300x300"}[side]
		img, format := thumb(t, files, a.ID, size)
		assert.Equal(t, "jpeg", format)
		assert.Equal(t, image.Rect(0, 0, side, side), img.Bounds(), size)
		for _, p := range []image.Point{{side / 2, side / 2}, {2, 2}, {side - 3, side - 3}} {
			r, g, _, _ := img.At(p.X, p.Y).RGBA()
			assert.True(t, g > r, "%s %v не из центра", size, p)
		}
	}
	assert.Equal(t, avatar.StatusCompleted, repo.rows[0].Status)
	assert.Equal(t, map[string]string{"100x100": "thumbnails/" + a.ID + "/100x100.jpg", "300x300": "thumbnails/" + a.ID + "/300x300.jpg"},
		repo.rows[0].ThumbnailKeys)
}

func TestProcessSmallAndFormats(t *testing.T) {
	for name, data := range map[string][]byte{"png": pngBytes(t, 64, 64), "jpeg": jpegBytes(t), "webp": []byte(webp1x1)} {
		svc, _, files, a := uploaded(t, data)
		require.NoError(t, svc.HandleUploadEvent(context.Background(), event(a)), name)
		for side, size := range map[int]string{100: "100x100", 300: "300x300"} {
			img, format := thumb(t, files, a.ID, size)
			assert.Equal(t, "jpeg", format, name)
			assert.Equal(t, image.Rect(0, 0, side, side), img.Bounds(), "%s %s", name, size)
		}
	}
}

func TestProcessRepeat(t *testing.T) {
	svc, _, files, a := uploaded(t, pngBytes(t, 2, 2))
	require.NoError(t, svc.HandleUploadEvent(context.Background(), event(a)))
	key := "thumbnails/" + a.ID + "/100x100.jpg"
	delete(files.objects, key)
	assert.ErrorIs(t, svc.HandleUploadEvent(context.Background(), event(a)), avatar.ErrDone)
	assert.NotContains(t, files.objects, key, "повтор тронул хранилище")
	assert.ErrorIs(t, svc.HandleUploadEvent(context.Background(), avatar.AvatarUploadEvent{AvatarID: "00000000-0000-0000-0000-000000000000"}), avatar.ErrDone)
}

func TestProcessPermanent(t *testing.T) {
	svc, repo, files, a := uploaded(t, pngBytes(t, 2, 2))
	repo.rows[0].Dimensions = avatar.Dimensions{Width: 30000, Height: 30000}
	files.fail = errBroken
	assert.ErrorIs(t, svc.HandleUploadEvent(context.Background(), event(a)), avatar.ErrPermanent, "огромную картинку читали")
	files.fail = nil
	repo.rows[0].Dimensions = avatar.Dimensions{Width: 2, Height: 2}
	files.objects[a.S3Key] = []byte("битый")
	assert.ErrorIs(t, svc.HandleUploadEvent(context.Background(), event(a)), avatar.ErrPermanent)
	require.NoError(t, svc.Fail(context.Background(), a.ID))
	assert.Equal(t, avatar.StatusFailed, repo.rows[0].Status)
	require.NoError(t, svc.Fail(context.Background(), a.ID), "повтор Fail дал ошибку")
}

func TestProcessTransient(t *testing.T) {
	svc, repo, files, a := uploaded(t, pngBytes(t, 2, 2))
	files.fail = errBroken
	assert.ErrorIs(t, svc.HandleUploadEvent(context.Background(), event(a)), errBroken)
	assert.NotErrorIs(t, svc.HandleUploadEvent(context.Background(), event(a)), avatar.ErrPermanent)
	assert.Equal(t, avatar.StatusProcessing, repo.rows[0].Status)
	files.fail = nil
	repo.fail = errBroken
	assert.ErrorIs(t, svc.HandleUploadEvent(context.Background(), event(a)), errBroken)
	assert.ErrorIs(t, svc.Fail(context.Background(), a.ID), errBroken)
}

func TestFailCompleted(t *testing.T) {
	svc, repo, _, a := uploaded(t, pngBytes(t, 2, 2))
	require.NoError(t, svc.HandleUploadEvent(context.Background(), event(a)))
	require.NoError(t, svc.Fail(context.Background(), a.ID))
	assert.Equal(t, avatar.StatusCompleted, repo.rows[0].Status)
}

func TestUploadPublishes(t *testing.T) {
	svc, repo, files, pub := withPublisher()
	a, err := svc.Upload(context.Background(), "alice", "me.png", pngBytes(t, 2, 2))
	require.NoError(t, err)
	assert.Equal(t, []any{avatar.AvatarUploadEvent{AvatarID: a.ID, UserID: "alice", S3Key: "originals/" + a.ID}}, pub.events)
	assert.Equal(t, []string{"avatars.exchange avatar.uploaded"}, pub.routes)
	pub.fail = errBroken
	_, err = svc.Upload(context.Background(), "alice", "me.png", pngBytes(t, 2, 2))
	assert.ErrorIs(t, err, avatar.ErrUnavailable)
	assert.ErrorIs(t, err, errBroken)
	assert.Len(t, repo.rows, 1, "запись не откатилась")
	assert.Len(t, files.objects, 1, "файл не откатился")
}

func TestReadThumbnail(t *testing.T) {
	svc, repo, files, a := uploaded(t, pngBytes(t, 2, 2))
	early, _, err := svc.Read(context.Background(), a, "100x100")
	require.NoError(t, err)
	require.NoError(t, svc.HandleUploadEvent(context.Background(), event(a)))
	assert.Equal(t, early, files.objects["thumbnails/"+a.ID+"/100x100.jpg"], "worker сделал другую миниатюру")
	data, mime, err := svc.Read(context.Background(), repo.rows[0], "100x100")
	require.NoError(t, err)
	assert.Equal(t, files.objects["thumbnails/"+a.ID+"/100x100.jpg"], data)
	assert.Equal(t, "image/jpeg", mime)
}

func TestProcessExifOrientation(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for x := range 200 {
		c := color.RGBA{R: 255, A: 255}
		if x >= 100 {
			c = color.RGBA{B: 255, A: 255}
		}
		for y := range 100 {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, nil))
	exif := []byte("\xff\xe1\x00\x22Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08\x00\x01\x01\x12\x00\x03\x00\x00\x00\x01\x00\x06\x00\x00\x00\x00\x00\x00")
	data := append(append([]byte{0xff, 0xd8}, exif...), buf.Bytes()[2:]...)
	svc, _, files, a := uploaded(t, data)
	require.NoError(t, svc.HandleUploadEvent(context.Background(), event(a)))
	got, _ := thumb(t, files, a.ID, "100x100")
	top, _, _, _ := got.At(50, 10).RGBA()
	_, _, bottom, _ := got.At(50, 90).RGBA()
	assert.True(t, top > 0xc000 && bottom > 0xc000, "миниатюра не повёрнута по EXIF")
}
