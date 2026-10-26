// Package postgres хранит метаданные аватарок в Postgres.
package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shigabutdinoff/gophprofile/internal/avatar"
)

const columns = `id::text, user_id, file_name, mime_type, size_bytes, width, height, s3_key, thumbnail_s3_keys,
	processing_status, created_at, updated_at`

// Avatars хранит метаданные аватарок в таблице avatars.
type Avatars struct {
	db *pgxpool.Pool
}

// New собирает хранилище метаданных над пулом db.
func New(db *pgxpool.Pool) *Avatars {
	return &Avatars{db: db}
}

// Create сохраняет a и заполняет её даты из записи.
func (r *Avatars) Create(ctx context.Context, a *avatar.Avatar) error {
	return r.db.QueryRow(ctx, `INSERT INTO avatars (id, user_id, file_name, mime_type, size_bytes, width, height, s3_key,
		upload_status, processing_status) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'uploaded', $9) RETURNING created_at, updated_at`,
		a.ID, a.UserID, a.FileName, a.MIMEType, a.Size, a.Dimensions.Width, a.Dimensions.Height, a.S3Key, string(a.Status)).Scan(&a.CreatedAt, &a.UpdatedAt)
}

// GetAvatar возвращает неудалённую аватарку id или avatar.ErrNotFound.
func (r *Avatars) GetAvatar(ctx context.Context, id string) (avatar.Avatar, error) {
	rows, _ := r.db.Query(ctx, "SELECT "+columns+" FROM avatars WHERE id = $1 AND deleted_at IS NULL", id)
	a, err := pgx.CollectExactlyOneRow(rows, scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, avatar.ErrNotFound
	}
	return a, err
}

// List возвращает не больше limit неудалённых аватарок пользователя userID,
// новые первыми. Нулевой limit снимает предел.
func (r *Avatars) List(ctx context.Context, userID string, limit int) ([]avatar.Avatar, error) {
	rows, _ := r.db.Query(ctx, "SELECT "+columns+` FROM avatars WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC, id DESC LIMIT NULLIF($2, 0)`, userID, limit)
	return pgx.CollectRows(rows, scan)
}

// UpdateProcessingStatus ставит status и ключи миниатюр keys аватарке id,
// которая ещё в обработке. Иначе возвращает avatar.ErrDone.
func (r *Avatars) UpdateProcessingStatus(ctx context.Context, id string, status avatar.Status, keys map[string]string) error {
	tag, err := r.db.Exec(ctx, `UPDATE avatars SET processing_status = $2, thumbnail_s3_keys = $3, updated_at = now()
		WHERE id = $1 AND processing_status = 'processing' AND deleted_at IS NULL`, id, string(status), keys)
	if err == nil && tag.RowsAffected() == 0 {
		err = avatar.ErrDone
	}
	return err
}

// MarkDeleted помечает удалённой аватарку id. Если записи нет или она уже
// помечена, возвращает avatar.ErrNotFound.
func (r *Avatars) MarkDeleted(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx, "UPDATE avatars SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL", id)
	if err == nil && tag.RowsAffected() == 0 {
		err = avatar.ErrNotFound
	}
	return err
}

// Delete удаляет запись аватарки id.
func (r *Avatars) Delete(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, "DELETE FROM avatars WHERE id = $1", id)
	return err
}

func scan(row pgx.CollectableRow) (avatar.Avatar, error) {
	var a avatar.Avatar
	err := row.Scan(&a.ID, &a.UserID, &a.FileName, &a.MIMEType, &a.Size, &a.Dimensions.Width, &a.Dimensions.Height, &a.S3Key, &a.ThumbnailKeys,
		&a.Status, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}
