package api

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/shigabutdinoff/gophprofile/internal/avatar"
)

func (h handler) upload(w http.ResponseWriter, r *http.Request) {
	file, head, err := r.FormFile("file")
	if errors.Is(err, http.ErrMissingFile) {
		file, head, err = r.FormFile("image")
	}
	var data []byte
	if err == nil {
		defer func() { _ = file.Close() }()
		data, err = io.ReadAll(file)
	}
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		fail(w, r, avatar.ErrTooLarge)
		return
	}
	if err != nil {
		fail(w, r, fmt.Errorf("%w: не передан файл", errBadRequest))
		return
	}
	a, err := h.svc.Upload(r.Context(), r.Header.Get("X-User-ID"), head.Filename, data)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, created{a.ID, a.UserID, "/api/v1/avatars/" + a.ID, a.Status, stamp(a.CreatedAt)})
}

func userID(r *http.Request) string {
	id := r.PathValue("user_id")
	if r.URL.RawPath != "" {
		id, _ = url.PathUnescape(id)
	}
	return id
}

func (h handler) file(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	size := cmp.Or(query.Get("size"), avatar.Original)
	format := query.Get("format")
	if query.Has("format") && !avatar.ValidFormat(format) || !avatar.ValidSize(size) {
		fail(w, r, fmt.Errorf("%w: неверный размер или формат", errBadRequest))
		return
	}
	id := r.PathValue("id")
	byID := id != ""
	var a avatar.Avatar
	var err error
	if byID {
		a, err = h.svc.Get(r.Context(), id)
	} else {
		a, err = h.svc.Current(r.Context(), userID(r))
	}
	var data []byte
	if err == nil {
		data, a.MIMEType, err = h.svc.Read(r.Context(), a, size)
	}
	if err == nil && format != "" {
		data, a.MIMEType, err = avatar.Convert(r.Context(), a, data, format)
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	cache := "no-cache"
	if byID {
		cache = "max-age=86400"
	}
	w.Header().Set("Cache-Control", cache)
	w.Header().Set("Content-Type", a.MIMEType)
	sum := sha256.Sum256(data)
	w.Header().Set("ETag", strconv.Quote(hex.EncodeToString(sum[:])))
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}
