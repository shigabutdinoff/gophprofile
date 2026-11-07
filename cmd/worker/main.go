// Command worker делает миниатюры и удаляет файлы аватарок по событиям из
// RabbitMQ, отдаёт пробу /live на HTTP_ADDRESS, по умолчанию порт 8080. Ловит
// SIGTERM, SIGINT и SIGQUIT и доводит текущее событие не дольше 10 с.
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
	"github.com/shigabutdinoff/gophprofile/internal/worker"
)

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
	beat := &worker.Beat{}
	mux := http.NewServeMux()
	mux.Handle("GET /live", api.Health(map[string]func(context.Context) error{"worker": beat.Live}))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(l) }()
	defer func() { _ = srv.Close() }()
	a, err := app.Open(ctx)
	if err != nil {
		return err
	}
	defer a.Close()
	deliveries, err := a.Broker.Consume()
	if err != nil {
		return fmt.Errorf("RABBITMQ_URL: %w", err)
	}
	worker.Run(ctx, deliveries, a.Service, 10*time.Second, beat)
	if ctx.Err() == nil {
		return errors.New("RABBITMQ_URL: соединение разорвано")
	}
	return nil
}
