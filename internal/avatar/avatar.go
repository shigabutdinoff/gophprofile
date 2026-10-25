// Package avatar описывает аватарку пользователя и сценарии её загрузки,
// чтения и удаления. Пакет не знает про HTTP, Postgres и S3, хранилища он
// получает через свои интерфейсы.
package avatar

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxSize это предел размера файла аватарки в байтах.
const MaxSize = 10 << 20

// Original это размер оригинала аватарки.
const Original = "original"

// Status это состояние обработки аватарки.
type Status string

const (
	// StatusProcessing отмечает аватарку, миниатюры которой ещё не готовы.
	StatusProcessing Status = "processing"
	// StatusCompleted отмечает аватарку с готовыми миниатюрами.
	StatusCompleted Status = "completed"
	// StatusFailed отмечает аватарку, миниатюры которой сделать не удалось.
	StatusFailed Status = "failed"
)

var (
	// ErrNotFound сообщает, что аватарки нет.
	ErrNotFound = errors.New("аватарка не найдена")
	// ErrInvalidFormat сообщает, что файл не JPEG, PNG или WebP.
	ErrInvalidFormat = errors.New("формат файла не поддерживается")
	// ErrTooLarge сообщает, что файл больше MaxSize.
	ErrTooLarge = errors.New("файл больше допустимого размера")
	// ErrUserID сообщает, что идентификатор пользователя пуст, не в UTF8,
	// содержит NUL или длиннее 255 символов.
	ErrUserID = errors.New("неверный идентификатор пользователя")
	// ErrUnavailable сообщает о временном сбое, после которого стоит повторить.
	ErrUnavailable = errors.New("сервис временно недоступен")
	// ErrForbidden сообщает, что аватарка принадлежит другому пользователю.
	ErrForbidden = errors.New("можно удалять только свои аватарки")
	// ErrDone сообщает, что аватарка уже не ждёт обработки.
	ErrDone = errors.New("аватарка уже обработана")
	// ErrPermanent отмечает сбой обработки, который повтор не исправит.
	ErrPermanent = errors.New("аватарку нельзя обработать")
)

// Dimensions это ширина и высота оригинала в пикселях.
type Dimensions struct {
	// Width содержит ширину в пикселях.
	Width int `json:"width"`
	// Height содержит высоту в пикселях.
	Height int `json:"height"`
}

// Avatar это метаданные аватарки.
type Avatar struct {
	// ID содержит UUID аватарки.
	ID string
	// UserID содержит идентификатор владельца аватарки.
	UserID string
	// FileName содержит имя загруженного файла не длиннее 255 символов.
	FileName string
	// MIMEType содержит тип файла, image/jpeg, image/png или image/webp.
	MIMEType string
	// Size содержит размер оригинала в байтах.
	Size int64
	// Dimensions содержит ширину и высоту оригинала.
	Dimensions Dimensions
	// S3Key содержит ключ оригинала в хранилище.
	S3Key string
	// ThumbnailKeys содержит ключи готовых миниатюр по их размерам.
	ThumbnailKeys map[string]string
	// Status содержит состояние обработки аватарки.
	Status Status
	// CreatedAt содержит время загрузки.
	CreatedAt time.Time
	// UpdatedAt содержит время последнего изменения записи.
	UpdatedAt time.Time
}

// ValidSize сообщает, поддерживает ли сервис размер size.
func ValidSize(size string) bool {
	return size == Original || size == "100x100" || size == "300x300"
}

func validUserID(id string) bool {
	return id != "" && utf8.ValidString(id) && !strings.ContainsRune(id, 0) && utf8.RuneCountInString(id) <= 255
}

func cleanName(name string) string {
	r := []rune(strings.ToValidUTF8(strings.ReplaceAll(name, "\x00", ""), ""))
	return string(r[:min(len(r), 255)])
}
