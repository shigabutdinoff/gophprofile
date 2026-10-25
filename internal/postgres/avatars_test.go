package postgres_test

import (
	"context"
	"crypto/rand"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/suite"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/shigabutdinoff/gophprofile/internal/avatar"
	"github.com/shigabutdinoff/gophprofile/internal/postgres"
)

type avatarsSuite struct {
	suite.Suite
	base string
	db   *pgxpool.Pool
	repo *postgres.Avatars
}

func TestAvatars(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	suite.Run(t, new(avatarsSuite))
}

func (s *avatarsSuite) SetupSuite() {
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:17-alpine", tcpostgres.BasicWaitStrategies())
	testcontainers.CleanupContainer(s.T(), ctr)
	s.Require().NoError(err, "контейнер Postgres не запущен")
	s.base, err = ctr.ConnectionString(ctx, "sslmode=disable")
	s.Require().NoError(err, "адрес базы не получен")
}

func (s *avatarsSuite) SetupTest() {
	t, ctx := s.T(), context.Background()
	admin, err := pgxpool.New(ctx, s.base)
	s.Require().NoError(err, "база не открыта")
	t.Cleanup(admin.Close)
	schema := "t_" + strings.ToLower(rand.Text())
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	s.Require().NoError(err, "схема не создана")
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") })
	u, err := url.Parse(s.base)
	s.Require().NoError(err, "адрес базы не разобран")
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	s.db, err = pgxpool.New(ctx, u.String())
	s.Require().NoError(err, "база не открыта")
	t.Cleanup(s.db.Close)
	migration, err := os.ReadFile("../../migrations/000001_create_avatars.up.sql")
	s.Require().NoError(err, "миграция не прочитана")
	_, err = s.db.Exec(ctx, string(migration))
	s.Require().NoError(err, "миграция не применена")
	s.repo = postgres.New(s.db)
}

func newAvatar(user, name string) avatar.Avatar {
	return avatar.Avatar{ID: uuid.New().String(), UserID: user, FileName: name, MIMEType: "image/png",
		Size: 2048, Dimensions: avatar.Dimensions{Width: 1920, Height: 1080}, S3Key: "originals/" + name, Status: avatar.StatusProcessing}
}

func (s *avatarsSuite) TestCreateGet() {
	ctx := context.Background()
	a := newAvatar("alice", "me.png")
	s.Require().NoError(s.repo.Create(ctx, &a), "запись не создана")
	s.False(a.CreatedAt.IsZero(), "дата не получена из базы")
	s.True(a.UpdatedAt.Equal(a.CreatedAt), "получены даты %v и %v, ожидались равные", a.CreatedAt, a.UpdatedAt)
	got, err := s.repo.GetAvatar(ctx, a.ID)
	s.Require().NoError(err, "запись не прочитана")
	got.CreatedAt, got.UpdatedAt = got.CreatedAt.UTC(), got.UpdatedAt.UTC()
	a.CreatedAt, a.UpdatedAt = a.CreatedAt.UTC(), a.UpdatedAt.UTC()
	s.Equal(a, got)
	_, err = s.repo.GetAvatar(ctx, uuid.New().String())
	s.ErrorIs(err, avatar.ErrNotFound)
}

func (s *avatarsSuite) TestList() {
	ctx := context.Background()
	empty, err := s.repo.List(ctx, "alice", 0)
	s.Require().NoError(err)
	s.Equal([]avatar.Avatar{}, empty, "ожидался пустой не nil список")
	older, newer, other := newAvatar("alice", "a.png"), newAvatar("alice", "b.png"), newAvatar("bob", "c.png")
	for _, a := range []*avatar.Avatar{&older, &newer, &other} {
		s.Require().NoError(s.repo.Create(ctx, a), "запись не создана")
		time.Sleep(time.Millisecond)
	}
	list, err := s.repo.List(ctx, "alice", 0)
	s.Require().NoError(err)
	s.Equal([]string{newer.ID, older.ID}, ids(list), "ожидались b.png и a.png")
	list, err = s.repo.List(ctx, "alice", 1)
	s.Require().NoError(err)
	s.Equal([]string{newer.ID}, ids(list), "предел 1 не соблюдён")
	_, err = s.db.Exec(ctx, "UPDATE avatars SET deleted_at = now() WHERE id = $1", newer.ID)
	s.Require().NoError(err, "пометка удаления не записана")
	_, err = s.repo.GetAvatar(ctx, newer.ID)
	s.ErrorIs(err, avatar.ErrNotFound, "удалённая запись видна по id")
	list, err = s.repo.List(ctx, "alice", 0)
	s.Require().NoError(err)
	s.Equal([]string{older.ID}, ids(list), "удалённая запись видна в списке")
}

func ids(list []avatar.Avatar) []string {
	out := make([]string, len(list))
	for i, a := range list {
		out[i] = a.ID
	}
	return out
}

func (s *avatarsSuite) TestLongFileName() {
	a := newAvatar("alice", strings.Repeat("я", 255))
	s.Require().NoError(s.repo.Create(context.Background(), &a), "запись не создана")
	got, err := s.repo.GetAvatar(context.Background(), a.ID)
	s.Require().NoError(err)
	s.Equal(strings.Repeat("я", 255), got.FileName)
}

func (s *avatarsSuite) TestClosedPool() {
	s.db.Close()
	_, err := s.repo.List(context.Background(), "alice", 0)
	s.Error(err, "список из закрытой базы прочитан без ошибки")
	_, err = s.repo.GetAvatar(context.Background(), uuid.New().String())
	s.Require().Error(err)
	s.NotErrorIs(err, avatar.ErrNotFound, "ожидалась ошибка базы")
}

func (s *avatarsSuite) TestUpdateProcessingStatus() {
	ctx := context.Background()
	a := newAvatar("alice", "me.png")
	s.Require().NoError(s.repo.Create(ctx, &a), "запись не создана")
	time.Sleep(10 * time.Millisecond)
	keys := map[string]string{"100x100": "thumbnails/a/100x100.jpg"}
	s.Require().NoError(s.repo.UpdateProcessingStatus(ctx, a.ID, avatar.StatusCompleted, keys))
	got, err := s.repo.GetAvatar(ctx, a.ID)
	s.Require().NoError(err)
	s.Equal(avatar.StatusCompleted, got.Status)
	s.Equal(keys, got.ThumbnailKeys)
	s.True(got.UpdatedAt.After(a.UpdatedAt), "updated_at не обновлён")
	s.ErrorIs(s.repo.UpdateProcessingStatus(ctx, a.ID, avatar.StatusFailed, nil), avatar.ErrDone)
	got, err = s.repo.GetAvatar(ctx, a.ID)
	s.Require().NoError(err)
	s.Equal(avatar.StatusCompleted, got.Status, "статус готовой аватарки изменён")
}

func (s *avatarsSuite) TestDelete() {
	ctx := context.Background()
	a := newAvatar("alice", "me.png")
	s.Require().NoError(s.repo.Create(ctx, &a), "запись не создана")
	s.Require().NoError(s.repo.Delete(ctx, a.ID))
	_, err := s.repo.GetAvatar(ctx, a.ID)
	s.ErrorIs(err, avatar.ErrNotFound)
}
