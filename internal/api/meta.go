package api

import (
	"net/http"
	"time"

	"github.com/shigabutdinoff/gophprofile/internal/avatar"
)

type created struct {
	ID        string        `json:"id"`
	UserID    string        `json:"user_id"`
	URL       string        `json:"url"`
	Status    avatar.Status `json:"status"`
	CreatedAt time.Time     `json:"created_at"`
}

type thumbnail struct {
	Size string `json:"size"`
	URL  string `json:"url"`
}

type metadata struct {
	ID         string            `json:"id"`
	UserID     string            `json:"user_id"`
	FileName   string            `json:"file_name"`
	MIMEType   string            `json:"mime_type"`
	Size       int64             `json:"size"`
	Dimensions avatar.Dimensions `json:"dimensions"`
	Thumbnails []thumbnail       `json:"thumbnails"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Status     avatar.Status     `json:"status"`
}

func stamp(t time.Time) time.Time {
	return t.UTC().Truncate(time.Second)
}

func describe(a avatar.Avatar) metadata {
	thumbs := []thumbnail{}
	for _, size := range []string{"100x100", "300x300"} {
		if a.ThumbnailKeys[size] != "" {
			thumbs = append(thumbs, thumbnail{size, "/api/v1/avatars/" + a.ID + "?size=" + size})
		}
	}
	return metadata{a.ID, a.UserID, a.FileName, a.MIMEType, a.Size, a.Dimensions, thumbs, stamp(a.CreatedAt), stamp(a.UpdatedAt), a.Status}
}

func (h handler) metadata(w http.ResponseWriter, r *http.Request) {
	a, err := h.svc.Get(r.Context(), r.PathValue("id"))
	respond(w, r, describe(a), err)
}

func (h handler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.List(r.Context(), userID(r))
	out := make([]metadata, len(list))
	for i, a := range list {
		out[i] = describe(a)
	}
	respond(w, r, out, err)
}
