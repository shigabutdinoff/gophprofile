package avatar

import (
	"cmp"
	"context"
	"fmt"
	"image"
	"uuid"
)

// Get возвращает аватарку id. Неверный id даёт ErrNotFound.
func (s *Service) Get(ctx context.Context, id string) (Avatar, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return Avatar{}, ErrNotFound
	}
	return s.repo.GetAvatar(ctx, parsed.String())
}

// List возвращает аватарки пользователя userID, новые первыми.
// Неверный идентификатор даёт пустой список.
func (s *Service) List(ctx context.Context, userID string) ([]Avatar, error) {
	if !validUserID(userID) {
		return []Avatar{}, nil
	}
	return s.repo.List(ctx, userID, 0)
}

// Current возвращает последнюю аватарку пользователя userID или ErrNotFound.
func (s *Service) Current(ctx context.Context, userID string) (Avatar, error) {
	if !validUserID(userID) {
		return Avatar{}, ErrNotFound
	}
	list, err := s.repo.List(ctx, userID, 1)
	if err != nil || len(list) == 0 {
		return Avatar{}, cmp.Or(err, ErrNotFound)
	}
	return list[0], nil
}

// Read читает файл размера size аватарки a и возвращает его MIME тип. Пока
// миниатюры нет, она делается из оригинала. Картинка больше MaxPixels даёт
// ErrTooManyPixels.
func (s *Service) Read(ctx context.Context, a Avatar, size string) ([]byte, string, error) {
	if key := a.ThumbnailKeys[size]; key != "" {
		data, err := s.files.Download(ctx, key, MaxSize)
		return data, "image/jpeg", err
	}
	data, err := s.files.Download(ctx, a.S3Key, MaxSize)
	if err != nil || size == Original || !ValidSize(size) {
		return data, a.MIMEType, err
	}
	var w, h int
	_, _ = fmt.Sscanf(size, "%dx%d", &w, &h)
	data, err = transcode(ctx, a, data, "jpeg", func(img image.Image) image.Image { return Resize(img, w, h) })
	return data, "image/jpeg", err
}
