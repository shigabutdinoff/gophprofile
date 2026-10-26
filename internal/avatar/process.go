package avatar

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
)

func thumbnails() []ProcessingOp { return []ProcessingOp{{100, 100}, {300, 300}} }

// HandleUploadEvent делает JPEG миниатюры 100x100 и 300x300 из оригинала
// события e и ставит аватарке StatusCompleted. Аватарка не в обработке даёт
// ErrDone, а картинка, которую нельзя обработать, ошибку с ErrPermanent.
func (s *Service) HandleUploadEvent(ctx context.Context, e AvatarUploadEvent) error {
	a, err := s.repo.GetAvatar(ctx, e.AvatarID)
	if errors.Is(err, ErrNotFound) {
		return s.gone(ctx, e.AvatarID)
	}
	if err == nil && a.Status != StatusProcessing {
		return ErrDone
	}
	if err != nil {
		return err
	}
	release, err := s.acquire(ctx, a)
	if errors.Is(err, ErrTooManyPixels) {
		return fmt.Errorf("%w: %w", ErrPermanent, err)
	}
	if err != nil {
		return err
	}
	defer release()
	data, err := s.files.Download(ctx, e.S3Key, MaxSize)
	if err != nil {
		return err
	}
	img, err := decode(data)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPermanent, err)
	}
	return s.process(ctx, img, AvatarProcessEvent{AvatarID: a.ID, Operations: thumbnails()})
}

func (s *Service) process(ctx context.Context, img image.Image, e AvatarProcessEvent) error {
	keys := map[string]string{}
	for _, op := range e.Operations {
		size := fmt.Sprintf("%dx%d", op.Width, op.Height)
		keys[size] = thumbKey(e.AvatarID, op)
		thumb, err := encode(Resize(img, op.Width, op.Height), "jpeg")
		if err == nil {
			err = s.files.Upload(ctx, keys[size], thumb)
		}
		if err != nil {
			return err
		}
	}
	err := s.repo.UpdateProcessingStatus(ctx, e.AvatarID, StatusCompleted, keys)
	if errors.Is(err, ErrDone) {
		_, err = s.repo.GetAvatar(ctx, e.AvatarID)
		if errors.Is(err, ErrNotFound) {
			return s.gone(ctx, e.AvatarID)
		}
		if err == nil {
			err = ErrDone
		}
	}
	return err
}

func (s *Service) gone(ctx context.Context, id string) error {
	keys := make([]string, 0, len(thumbnails()))
	for _, op := range thumbnails() {
		keys = append(keys, thumbKey(id, op))
	}
	if err := s.drop(ctx, keys); err != nil {
		return err
	}
	return ErrDone
}

func thumbKey(id string, op ProcessingOp) string {
	return fmt.Sprintf("thumbnails/%s/%dx%d.jpg", id, op.Width, op.Height)
}

// Fail ставит StatusFailed аватарке id, если она ещё в обработке.
func (s *Service) Fail(ctx context.Context, id string) error {
	if err := s.repo.UpdateProcessingStatus(ctx, id, StatusFailed, nil); !errors.Is(err, ErrDone) {
		return err
	}
	return nil
}

func (s *Service) rollback(ctx context.Context, a Avatar, cause error) error {
	ctx = context.WithoutCancel(ctx)
	err := s.repo.Delete(ctx, a.ID)
	if err == nil {
		err = s.files.Delete(ctx, a.S3Key)
	}
	if err != nil {
		slog.ErrorContext(ctx, "откат загрузки не завершён", "id", a.ID, "err", err)
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, cause)
}
