package avatar

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"os"
	"slices"
	"time"
	"uuid"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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
	// UpdateProcessingStatus ставит status и ключи миниатюр keys аватарке id
	// в обработке, иначе возвращает ErrDone.
	UpdateProcessingStatus(ctx context.Context, id string, status Status, keys map[string]string) error
	// Delete удаляет запись аватарки id.
	Delete(ctx context.Context, id string) error
	// MarkDeleted помечает удалённой аватарку id. Если записи нет или она
	// уже помечена, возвращает ErrNotFound.
	MarkDeleted(ctx context.Context, id string) error
}

// Storage хранит файлы аватарок.
type Storage interface {
	// Upload кладёт data объектом key.
	Upload(ctx context.Context, key string, data []byte) error
	// Download читает объект key не длиннее limit байт.
	Download(ctx context.Context, key string, limit int64) ([]byte, error)
	// Delete удаляет объект key.
	Delete(ctx context.Context, key string) error
}

// Publisher передаёт события обработчику.
type Publisher interface {
	// Publish отправляет event в exchange с ключом key и возвращается, когда
	// брокер его принял.
	Publish(ctx context.Context, exchange, key string, event any) error
}

// Service выполняет сценарии загрузки, чтения и удаления аватарок.
type Service struct {
	repo      Repository
	files     Storage
	publisher Publisher
	slot      chan struct{}
	limit     int
	mimes     []string
}

// New собирает сервис над метаданными repo, файлами files и брокером
// publisher. Загрузка принимает файлы до limit байт с MIME типами mimes.
func New(repo Repository, files Storage, publisher Publisher, limit int, mimes []string) *Service {
	return &Service{repo: repo, files: files, publisher: publisher, slot: make(chan struct{}, 1), limit: limit, mimes: mimes}
}

// Upload проверяет и сохраняет файл data с именем name пользователя userID.
// Если запись могла сохраниться или событие не отправлено, запись и файл
// удаляются, а ошибка содержит ErrUnavailable. Сохранение видно в трейсах спаном upload_avatar.
func (s *Service) Upload(ctx context.Context, userID, name string, data []byte) (Avatar, error) {
	if !validUserID(userID) {
		return Avatar{}, ErrUserID
	}
	if len(data) > s.limit {
		return Avatar{}, ErrTooLarge
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err == nil && format == "webp" {
		err = sameFrame(data, cfg)
	}
	if err != nil || !slices.Contains(s.mimes, "image/"+format) {
		return Avatar{}, ErrInvalidFormat
	}
	id := uuid.New().String()
	a := Avatar{ID: id, UserID: userID, FileName: cleanName(name), MIMEType: "image/" + format, Size: int64(len(data)),
		Dimensions: Dimensions{cfg.Width, cfg.Height}, S3Key: "originals/" + id, Status: StatusProcessing}
	ctx, span := otel.Tracer("gophprofile").Start(ctx, "upload_avatar", trace.WithAttributes(attribute.String("user_id", userID),
		attribute.String("file_name", a.FileName), attribute.Int64("file_size", a.Size)))
	logger := slog.With("service", os.Getenv("OTEL_SERVICE_NAME"), "trace_id", trace.SpanFromContext(ctx).SpanContext().TraceID())
	logger.InfoContext(ctx, "uploading avatar", "user_id", userID, "file_size", a.Size, "mime_type", a.MIMEType)
	if err := s.files.Upload(ctx, a.S3Key, data); err != nil {
		return Avatar{}, end(span, err)
	}
	if err = s.repo.Create(ctx, &a); err != nil {
		if !errors.Is(err, ErrNotSaved) {
			// INSERT может закоммититься уже после ошибки, поэтому файл
			// остаётся: лишний файл лучше записи без файла.
			dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			if derr := s.repo.Delete(dctx, a.ID); derr != nil {
				slog.ErrorContext(ctx, "сомнительная запись не удалена", "id", a.ID, "err", derr)
			}
			cancel()
			return Avatar{}, end(span, fmt.Errorf("%w: %w", ErrUnavailable, err))
		}
		if derr := s.files.Delete(context.WithoutCancel(ctx), a.S3Key); derr != nil {
			slog.ErrorContext(ctx, "файл без записи не удалён", "key", a.S3Key, "err", derr)
		}
		return Avatar{}, end(span, err)
	}
	if err = s.PublishUploadEvent(ctx, a.ID, a.UserID, a.S3Key); err != nil {
		return Avatar{}, end(span, s.rollback(ctx, a, err))
	}
	return a, end(span, nil)
}
