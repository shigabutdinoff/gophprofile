package avatar

import (
	"context"
	"errors"
	"fmt"
)

// Delete публикует событие удаления аватарки id пользователя userID и
// помечает её удалённой. Чужая даёт ErrForbidden, отсутствующая ErrNotFound,
// а непринятое брокером событие оставляет аватарку и даёт ErrUnavailable.
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	if !validUserID(userID) {
		return ErrUserID
	}
	a, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	return s.remove(ctx, userID, a)
}

// DeleteCurrent удаляет текущую аватарку пользователя owner по запросу
// пользователя userID с теми же ошибками, что Delete.
func (s *Service) DeleteCurrent(ctx context.Context, userID, owner string) error {
	if !validUserID(userID) {
		return ErrUserID
	}
	a, err := s.Current(ctx, owner)
	if err != nil {
		return err
	}
	return s.remove(ctx, userID, a)
}

func (s *Service) remove(ctx context.Context, userID string, a Avatar) error {
	if a.UserID != userID {
		return ErrForbidden
	}
	ctx = context.WithoutCancel(ctx)
	keys := []string{a.S3Key}
	for _, op := range thumbnails {
		keys = append(keys, thumbKey(a.ID, op))
	}
	if err := s.PublishDeleteEvent(ctx, a.ID, keys); err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return s.repo.MarkDeleted(ctx, a.ID)
}

// HandleDeleteEvent удаляет из хранилища файлы события e. Если аватарка ещё
// не помечена удалённой, файлы остаются и возвращается ошибка с
// ErrUnavailable, чтобы событие повторили.
func (s *Service) HandleDeleteEvent(ctx context.Context, e AvatarDeleteEvent) error {
	_, err := s.repo.GetAvatar(ctx, e.AvatarID)
	if err == nil {
		return fmt.Errorf("%w: аватарка %s ещё не помечена удалённой", ErrUnavailable, e.AvatarID)
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	return s.drop(ctx, e.S3Keys)
}

func (s *Service) drop(ctx context.Context, keys []string) error {
	for _, key := range keys {
		if err := s.files.Delete(ctx, key); err != nil {
			return err
		}
	}
	return nil
}
