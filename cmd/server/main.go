// Command server отдаёт REST API аватарок GophProfile. Нужны переменные
// окружения из пакета app, адрес прослушивания задаёт HTTP_ADDRESS, по
// умолчанию порт 8080. Останавливается по SIGTERM, SIGINT и SIGQUIT.
package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shigabutdinoff/gophprofile/internal/api"
	"github.com/shigabutdinoff/gophprofile/internal/app"
)

const grace = 10 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatal("Ошибка: ", err)
	}
}

func run(ctx context.Context) error {
	l, err := net.Listen("tcp", cmp.Or(os.Getenv("HTTP_ADDRESS"), ":8080"))
	if err != nil {
		return fmt.Errorf("HTTP_ADDRESS: %w", err)
	}
	a, err := app.Open(ctx)
	if err != nil {
		_ = l.Close()
		return err
	}
	defer a.Close()
	srv := &http.Server{
		Handler:           api.New(a.Service, a.Health()),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Minute,
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       time.Minute,
	}
	return serve(ctx, srv, l, grace)
}

func serve(ctx context.Context, srv *http.Server, l net.Listener, grace time.Duration) error {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		wait, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		if srv.Shutdown(wait) != nil {
			_ = srv.Close()
		}
	})
	defer stop()
	if err := srv.Serve(l); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-done
	return nil
}
