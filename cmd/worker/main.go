// Command worker делает миниатюры аватарок GophProfile по событиям из
// RabbitMQ. Нужны переменные окружения из пакета app. Останавливается по
// SIGTERM, SIGINT и SIGQUIT, доводя текущее событие не дольше 10 с.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/signal"
	"syscall"
	"time"

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
	a, err := app.Open(ctx)
	if err != nil {
		return err
	}
	defer a.Close()
	deliveries, err := a.Broker.Consume()
	if err != nil {
		return fmt.Errorf("RABBITMQ_URL: %w", err)
	}
	worker.Run(ctx, deliveries, a.Service, 10*time.Second)
	if ctx.Err() == nil {
		return errors.New("RABBITMQ_URL: соединение разорвано")
	}
	return nil
}
