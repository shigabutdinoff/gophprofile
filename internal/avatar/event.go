package avatar

import "context"

// AvatarUploadEvent это событие загрузки аватарки в форме сообщения брокера.
type AvatarUploadEvent struct {
	// AvatarID содержит UUID аватарки.
	AvatarID string `json:"avatar_id"`
	// UserID содержит идентификатор владельца.
	UserID string `json:"user_id"`
	// S3Key содержит ключ оригинала в хранилище.
	S3Key string `json:"s3_key"`
}

// AvatarProcessEvent это задание сделать миниатюры аватарки.
type AvatarProcessEvent struct {
	// AvatarID содержит UUID аватарки.
	AvatarID string `json:"avatar_id"`
	// Operations содержит миниатюры, которые нужно сделать.
	Operations []ProcessingOp `json:"operations"`
}

// ProcessingOp это миниатюра, вырезанная по центру оригинала.
type ProcessingOp struct {
	// Width содержит ширину миниатюры в пикселях.
	Width int `json:"width"`
	// Height содержит высоту миниатюры в пикселях.
	Height int `json:"height"`
}

// PublishUploadEvent публикует событие загрузки оригинала s3Key аватарки
// avatarID пользователя userID.
func (s *Service) PublishUploadEvent(ctx context.Context, avatarID, userID, s3Key string) error {
	return s.publisher.Publish(ctx, "avatars.exchange", "avatar.uploaded", AvatarUploadEvent{avatarID, userID, s3Key})
}
