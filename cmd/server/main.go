// Command server отдаёт REST API аватарок GophProfile. Нужны переменные
// окружения DATABASE_URL, S3_ENDPOINT, S3_ACCESS_KEY и S3_SECRET_KEY.
// Адрес прослушивания задаёт HTTP_ADDRESS, по умолчанию порт 8080.
package main

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shigabutdinoff/gophprofile/internal/api"
	"github.com/shigabutdinoff/gophprofile/internal/avatar"
	"github.com/shigabutdinoff/gophprofile/internal/postgres"
	"github.com/shigabutdinoff/gophprofile/internal/storage"
)

func main() {
	if err := run(); err != nil {
		log.Fatal("Ошибка: ", err)
	}
}

func run() error {
	var missing []string
	for _, key := range []string{"DATABASE_URL", "S3_ENDPOINT", "S3_ACCESS_KEY", "S3_SECRET_KEY"} {
		if os.Getenv(key) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("не заданы %s", strings.Join(missing, ", "))
	}
	endpoint := os.Getenv("S3_ENDPOINT")
	if u, err := url.Parse(endpoint); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("неверная S3_ENDPOINT %q, нужна схема %s или %s", endpoint, "http", "https")
	}
	db, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("DATABASE_URL: %w", err)
	}
	defer db.Close()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 64
	files := storage.New(&http.Client{Transport: transport}, endpoint, os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_SECRET_KEY"))
	srv := &http.Server{
		Addr:              cmp.Or(os.Getenv("HTTP_ADDRESS"), ":8080"),
		Handler:           api.New(avatar.New(postgres.New(db), files)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Minute,
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       time.Minute,
	}
	return fmt.Errorf("HTTP_ADDRESS: %w", srv.ListenAndServe())
}
