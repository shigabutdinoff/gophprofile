package avatar

import (
	"bytes"
	"image"

	"golang.org/x/image/riff"
	"golang.org/x/image/vp8"
	"golang.org/x/image/vp8l"
)

func sameFrame(data []byte, cfg image.Config) error {
	_, r, err := riff.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	for {
		id, n, chunk, err := r.Next()
		if err != nil {
			return err
		}
		var frame image.Config
		switch id {
		case riff.FourCC{'V', 'P', '8', ' '}:
			d := vp8.NewDecoder()
			d.Init(chunk, int(n))
			fh, err := d.DecodeFrameHeader()
			if err != nil {
				return err
			}
			frame.Width, frame.Height = fh.Width, fh.Height
		case riff.FourCC{'V', 'P', '8', 'L'}:
			if frame, err = vp8l.DecodeConfig(chunk); err != nil {
				return err
			}
		default:
			continue
		}
		if frame.Width != cfg.Width || frame.Height != cfg.Height {
			return ErrInvalidFormat
		}
		return nil
	}
}
