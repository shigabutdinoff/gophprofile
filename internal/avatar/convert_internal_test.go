package avatar

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConvertBusySlot(t *testing.T) {
	s := New(nil, nil, nil)
	s.slot <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	a := Avatar{MIMEType: "image/png", Dimensions: Dimensions{Width: 1, Height: 1}}
	_, _, err := s.Convert(ctx, a, []byte("png"), "jpeg")
	assert.ErrorIs(t, err, context.Canceled, "занятый слот ждали после отмены")
}
