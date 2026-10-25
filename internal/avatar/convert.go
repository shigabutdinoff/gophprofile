package avatar

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"

	"github.com/HugoSmits86/nativewebp"
	"github.com/disintegration/imaging"
)

// MaxPixels это предел числа пикселей картинки, которую сервис перекодирует.
const MaxPixels = 25_000_000

// ErrTooManyPixels сообщает, что картинка больше MaxPixels пикселей.
var ErrTooManyPixels = errors.New("картинка слишком большая для перекодирования")

// ValidFormat сообщает, умеет ли сервис отдавать картинку в формате format.
func ValidFormat(format string) bool {
	return format == "jpeg" || format == "png" || format == "webp"
}

// Convert перекодирует оригинал data аватарки a в формат format и возвращает
// его MIME тип. Оригинал в том же формате возвращается как есть. Картинка
// больше MaxPixels даёт ErrTooManyPixels.
func Convert(ctx context.Context, a Avatar, data []byte, format string) ([]byte, string, error) {
	mime := "image/" + format
	if mime == a.MIMEType {
		return data, mime, nil
	}
	out, err := transcode(ctx, a, data, format, func(img image.Image) image.Image { return img })
	return out, mime, err
}

var slot = make(chan struct{}, 1)

func acquire(ctx context.Context, a Avatar) (func(), error) {
	if a.Dimensions.Width*a.Dimensions.Height > MaxPixels {
		return nil, ErrTooManyPixels
	}
	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func transcode(ctx context.Context, a Avatar, data []byte, format string, change func(image.Image) image.Image) ([]byte, error) {
	release, err := acquire(ctx, a)
	if err != nil {
		return nil, err
	}
	defer release()
	img, err := decode(data)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return encode(change(img), format)
}

func decode(data []byte) (image.Image, error) {
	return imaging.Decode(bytes.NewReader(data), imaging.AutoOrientation(true))
}

type onWhite struct{ image.Image }

func (onWhite) ColorModel() color.Model { return color.RGBA64Model }

func (w onWhite) At(x, y int) color.Color {
	r, g, b, a := w.Image.At(x, y).RGBA()
	k := 0xffff - a
	return color.RGBA64{uint16(r + k), uint16(g + k), uint16(b + k), 0xffff}
}

func encode(img image.Image, format string) ([]byte, error) {
	var buf bytes.Buffer
	var err error
	switch format {
	case "jpeg":
		err = jpeg.Encode(&buf, onWhite{img}, nil)
	case "png":
		err = png.Encode(&buf, img)
	default:
		err = nativewebp.Encode(&buf, img, nil)
	}
	return buf.Bytes(), err
}

// Resize вырезает из img по центру часть с пропорциями width к height и
// масштабирует её до width на height пикселей.
func Resize(img image.Image, width, height int) image.Image {
	b := img.Bounds()
	side := min(b.Dx()*height, b.Dy()*width)
	crop := imaging.CropCenter(img, max(1, side/height), max(1, side/width))
	return imaging.Resize(crop, width, height, imaging.CatmullRom)
}
