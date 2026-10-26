package main

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcrabbit "github.com/testcontainers/testcontainers-go/modules/rabbitmq"
)

func setEnv(t *testing.T, rabbit string) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://postgres:секрет-базы@127.0.0.1:1/postgres")
	t.Setenv("S3_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("S3_ACCESS_KEY", "minio")
	t.Setenv("S3_SECRET_KEY", "секрет")
	t.Setenv("RABBITMQ_URL", rabbit)
}

func TestRunMissing(t *testing.T) {
	setEnv(t, "")
	assert.EqualError(t, run(context.Background()), "не заданы RABBITMQ_URL")
}

func TestRunStops(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctr, err := tcrabbit.Run(context.Background(), "rabbitmq:4.4.0-management-alpine")
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err, "контейнер RabbitMQ не запущен")
	url, err := ctr.AmqpURL(context.Background())
	require.NoError(t, err)
	setEnv(t, url)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	time.Sleep(500 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err, "остановка с ошибкой")
	case <-time.After(15 * time.Second):
		require.Fail(t, "обработчик не остановился")
	}
}

func TestMainFails(t *testing.T) {
	if os.Getenv("RUN_MAIN") == "1" {
		main()
		return
	}
	setEnv(t, "")
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainFails$")
	cmd.Env = append(os.Environ(), "RUN_MAIN=1")
	out, err := cmd.CombinedOutput()
	require.Error(t, err, "программа не упала")
	assert.Contains(t, string(out), "Ошибка: не заданы RABBITMQ_URL")
}
