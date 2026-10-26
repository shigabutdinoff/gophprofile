package avatar_test

import (
	"bytes"
	"image"
	"image/png"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophprofile/internal/avatar"
)

func TestConvert(t *testing.T) {
	svc, _, _ := newService()
	orig := pngBytes(t, 3, 2)
	a := avatar.Avatar{MIMEType: "image/png", Dimensions: avatar.Dimensions{Width: 3, Height: 2}}
	for _, format := range []string{"jpeg", "webp"} {
		data, mime, err := svc.Convert(t.Context(), a, orig, format)
		require.NoError(t, err, format)
		assert.Equal(t, "image/"+format, mime)
		cfg, got, err := image.DecodeConfig(bytes.NewReader(data))
		require.NoError(t, err, format)
		assert.Equal(t, format, got)
		assert.Equal(t, 3, cfg.Width, format)
	}
	same, mime, err := svc.Convert(t.Context(), a, orig, "png")
	require.NoError(t, err)
	assert.Equal(t, "image/png", mime)
	assert.Equal(t, orig, same, "тот же формат ожидался без перекодирования")
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 3, 2))))
	data, _, err := svc.Convert(t.Context(), a, buf.Bytes(), "jpeg")
	require.NoError(t, err)
	img, _, err := image.Decode(bytes.NewReader(data))
	require.NoError(t, err)
	r, g, b, _ := img.At(1, 1).RGBA()
	assert.True(t, r > 0xf000 && g > 0xf000 && b > 0xf000, "прозрачное стало не белым")
	_, _, err = svc.Convert(t.Context(), a, []byte("битый"), "jpeg")
	assert.Error(t, err, "битый файл перекодирован")
	a.Dimensions = avatar.Dimensions{Width: 10000, Height: 10000}
	_, _, err = svc.Convert(t.Context(), a, orig, "jpeg")
	assert.ErrorIs(t, err, avatar.ErrTooManyPixels)
}

func TestResizeNarrow(t *testing.T) {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	thumb := avatar.Resize(image.NewNRGBA(image.Rect(0, 0, 1, 2000)), 300, 300)
	runtime.ReadMemStats(&after)
	assert.Equal(t, image.Rect(0, 0, 300, 300), thumb.Bounds())
	assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(10<<20), "узкая картинка растянута целиком")
}

func TestValidFormat(t *testing.T) {
	for _, f := range []string{"jpeg", "png", "webp"} {
		assert.True(t, avatar.ValidFormat(f), f)
	}
	for _, f := range []string{"", "gif", "jpg"} {
		assert.False(t, avatar.ValidFormat(f), f)
	}
}
