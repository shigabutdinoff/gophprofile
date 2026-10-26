package avatar_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophprofile/internal/avatar"
)

func published(t *testing.T) (*avatar.Service, *fakeRepo, *fakeFiles, *fakePub, avatar.Avatar) {
	t.Helper()
	svc, repo, files, pub := withPublisher()
	a, err := svc.Upload(context.Background(), "alice", "me.png", pngBytes(t, 2, 2))
	require.NoError(t, err, "загрузка не прошла")
	pub.events, pub.routes = nil, nil
	return svc, repo, files, pub, a
}

func keys(id string) []string {
	return []string{"originals/" + id, "thumbnails/" + id + "/100x100.jpg", "thumbnails/" + id + "/300x300.jpg"}
}

func TestDeleteOwn(t *testing.T) {
	svc, _, files, pub, a := published(t)
	require.NoError(t, svc.HandleUploadEvent(context.Background(), event(a)))
	require.NoError(t, svc.Delete(context.Background(), "alice", a.ID))
	_, err := svc.Get(context.Background(), a.ID)
	assert.ErrorIs(t, err, avatar.ErrNotFound, "удалённая аватарка читается")
	assert.Equal(t, []string{"avatars.exchange avatar.deleted"}, pub.routes)
	require.Equal(t, []any{avatar.AvatarDeleteEvent{AvatarID: a.ID, S3Keys: keys(a.ID)}}, pub.events)
	require.NoError(t, svc.HandleDeleteEvent(context.Background(), pub.events[0].(avatar.AvatarDeleteEvent)))
	assert.Empty(t, files.objects, "файлы остались в хранилище")
	assert.NoError(t, svc.HandleDeleteEvent(context.Background(), pub.events[0].(avatar.AvatarDeleteEvent)), "повтор события дал ошибку")
}

func TestDeleteForeign(t *testing.T) {
	svc, _, _, pub, a := published(t)
	assert.ErrorIs(t, svc.Delete(context.Background(), "bob", a.ID), avatar.ErrForbidden)
	_, err := svc.Get(context.Background(), a.ID)
	assert.NoError(t, err, "чужая аватарка удалена")
	assert.Empty(t, pub.events, "событие опубликовано")
	assert.ErrorIs(t, svc.DeleteCurrent(context.Background(), "bob", "alice"), avatar.ErrForbidden)
}

func TestDeleteCurrent(t *testing.T) {
	svc, _, _, _, a := published(t)
	b, err := svc.Upload(context.Background(), "alice", "b.png", pngBytes(t, 2, 2))
	require.NoError(t, err)
	require.NoError(t, svc.DeleteCurrent(context.Background(), "alice", "alice"))
	_, err = svc.Get(context.Background(), b.ID)
	assert.ErrorIs(t, err, avatar.ErrNotFound, "удалена не последняя аватарка")
	current, err := svc.Current(context.Background(), "alice")
	require.NoError(t, err)
	assert.Equal(t, a.ID, current.ID, "текущей не стала предыдущая")
	require.NoError(t, svc.DeleteCurrent(context.Background(), "alice", "alice"))
	assert.ErrorIs(t, svc.DeleteCurrent(context.Background(), "alice", "alice"), avatar.ErrNotFound)
}

func TestDeleteAgain(t *testing.T) {
	svc, _, _, pub, a := published(t)
	require.NoError(t, svc.Delete(context.Background(), "alice", a.ID))
	assert.ErrorIs(t, svc.Delete(context.Background(), "alice", a.ID), avatar.ErrNotFound)
	assert.ErrorIs(t, svc.Delete(context.Background(), "bob", a.ID), avatar.ErrNotFound, "удалённая аватарка видна чужому")
	assert.ErrorIs(t, svc.Delete(context.Background(), "alice", "не uuid"), avatar.ErrNotFound)
	assert.Len(t, pub.events, 1, "повтор опубликовал событие")
}

func TestDeleteUserID(t *testing.T) {
	svc, _, _, pub, a := published(t)
	for _, user := range []string{"", "пользователь", strings.Repeat("a", 256)} {
		assert.ErrorIs(t, svc.Delete(context.Background(), user, a.ID), avatar.ErrUserID)
		assert.ErrorIs(t, svc.DeleteCurrent(context.Background(), user, "alice"), avatar.ErrUserID)
	}
	assert.Empty(t, pub.events)
}

func TestDeleteUnavailable(t *testing.T) {
	svc, _, _, pub, a := published(t)
	pub.fail = errBroken
	ctx, cancel := context.WithCancel(context.Background())
	pub.during = cancel
	err := svc.Delete(ctx, "alice", a.ID)
	assert.ErrorIs(t, err, avatar.ErrUnavailable)
	assert.ErrorIs(t, err, errBroken)
	assert.NoError(t, pub.ctxErr, "публикация получила отменённый контекст")
	_, err = svc.Get(context.Background(), a.ID)
	assert.NoError(t, err, "пометка удаления не снята")
}

func TestDeleteMarkAfterPublish(t *testing.T) {
	svc, repo, _, pub, a := published(t)
	repo.onMark = func() {
		assert.Len(t, pub.events, 1, "пометка поставлена до публикации")
		repo.fail = errBroken
	}
	assert.ErrorIs(t, svc.Delete(context.Background(), "alice", a.ID), errBroken)
	repo.fail = nil
	_, err := svc.Get(context.Background(), a.ID)
	assert.NoError(t, err, "аватарка скрыта без пометки")
}

func TestHandleDeleteLive(t *testing.T) {
	svc, _, files, _, a := published(t)
	e := avatar.AvatarDeleteEvent{AvatarID: a.ID, S3Keys: keys(a.ID)}
	assert.ErrorIs(t, svc.HandleDeleteEvent(context.Background(), e), avatar.ErrUnavailable, "событие до пометки не повторится")
	assert.Contains(t, files.objects, a.S3Key, "файл живой аватарки удалён")
	require.NoError(t, svc.Delete(context.Background(), "alice", a.ID))
	files.fail = errBroken
	assert.ErrorIs(t, svc.HandleDeleteEvent(context.Background(), e), errBroken)
}

func TestDeleteDuringProcessing(t *testing.T) {
	svc, repo, files, pub, a := published(t)
	files.onUpload = func(key string) {
		if strings.HasSuffix(key, "300x300.jpg") {
			require.NoError(t, svc.Delete(context.Background(), "alice", a.ID))
		}
	}
	assert.ErrorIs(t, svc.HandleUploadEvent(context.Background(), event(a)), avatar.ErrDone)
	assert.NotEqual(t, avatar.StatusCompleted, repo.rows[0].Status, "удалённая аватарка получила completed")
	require.NoError(t, svc.HandleDeleteEvent(context.Background(), pub.events[0].(avatar.AvatarDeleteEvent)))
	assert.Empty(t, files.objects, "файлы остались в хранилище")
}

func TestUploadEventAfterDelete(t *testing.T) {
	svc, _, files, _, a := published(t)
	require.NoError(t, svc.Delete(context.Background(), "alice", a.ID))
	files.objects["thumbnails/"+a.ID+"/100x100.jpg"] = []byte("часть")
	assert.ErrorIs(t, svc.HandleUploadEvent(context.Background(), event(a)), avatar.ErrDone)
	assert.NotContains(t, files.objects, "thumbnails/"+a.ID+"/100x100.jpg", "миниатюра удалённой аватарки осталась")
}

func TestProcessedByOther(t *testing.T) {
	svc, repo, files, _, a := published(t)
	files.onUpload = func(key string) {
		if strings.HasSuffix(key, "300x300.jpg") {
			repo.rows[0].Status = avatar.StatusCompleted
		}
	}
	assert.ErrorIs(t, svc.HandleUploadEvent(context.Background(), event(a)), avatar.ErrDone)
	assert.Contains(t, files.objects, "thumbnails/"+a.ID+"/300x300.jpg", "миниатюры обработанной аватарки удалены")
}
