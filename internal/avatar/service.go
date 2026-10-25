package avatar

import (
	"bytes"
	"context"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"uuid"

	"golang.org/x/image/riff"
	"golang.org/x/image/vp8"
	"golang.org/x/image/vp8l"
	_ "golang.org/x/image/webp"
)

// Repository хранит метаданные аватарок.
type Repository interface {
	// Create сохраняет a и заполняет её даты из записи.
	Create(ctx context.Context, a *Avatar) error
	// GetAvatar возвращает аватарку id или ErrNotFound.
	GetAvatar(ctx context.Context, id string) (Avatar, error)
	// List возвращает до limit аватарок userID новыми первыми, 0 без предела.
	List(ctx context.Context, userID string, limit int) ([]Avatar, error)
}

// Storage хранит файлы аватарок.
type Storage interface {
	// Upload кладёт data объектом key.
	Upload(ctx context.Context, key string, data []byte) error
	// Download читает объект key не длиннее limit байт.
	Download(ctx context.Context, key string, limit int64) ([]byte, error)
}

// Service выполняет сценарии загрузки и чтения аватарок.
type Service struct {
	repo  Repository
	files Storage
}

// New собирает сервис над метаданными repo и файлами files.
func New(repo Repository, files Storage) *Service {
	return &Service{repo: repo, files: files}
}

// Upload проверяет и сохраняет файл data с именем name пользователя userID.
// При сбое записи файл остаётся в хранилище.
func (s *Service) Upload(ctx context.Context, userID, name string, data []byte) (Avatar, error) {
	if !validUserID(userID) {
		return Avatar{}, ErrUserID
	}
	if len(data) > MaxSize {
		return Avatar{}, ErrTooLarge
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err == nil && format == "webp" {
		err = sameFrame(data, cfg)
	}
	if err != nil || !ValidFormat(format) {
		return Avatar{}, ErrInvalidFormat
	}
	id := uuid.New().String()
	a := Avatar{ID: id, UserID: userID, FileName: cleanName(name), MIMEType: "image/" + format, Size: int64(len(data)),
		Dimensions: Dimensions{cfg.Width, cfg.Height}, S3Key: "originals/" + id, Status: StatusProcessing}
	if err := s.files.Upload(ctx, a.S3Key, data); err != nil {
		return Avatar{}, err
	}
	err = s.repo.Create(ctx, &a)
	return a, err
}

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
