package main

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcrabbit "github.com/testcontainers/testcontainers-go/modules/rabbitmq"
)

func setEnv(t *testing.T, values map[string]string) {
	t.Helper()
	for _, key := range []string{"DATABASE_URL", "S3_ENDPOINT", "S3_ACCESS_KEY", "S3_SECRET_KEY", "RABBITMQ_URL"} {
		t.Setenv(key, values[key])
	}
	t.Setenv("HTTP_ADDRESS", "127.0.0.1:0")
	if addr, ok := values["HTTP_ADDRESS"]; ok {
		t.Setenv("HTTP_ADDRESS", addr)
	}
}

func full() map[string]string {
	return map[string]string{
		"DATABASE_URL":  "postgres://postgres:секрет-базы@127.0.0.1:1/postgres",
		"S3_ENDPOINT":   "http://127.0.0.1:1",
		"S3_ACCESS_KEY": "minio",
		"S3_SECRET_KEY": "секрет",
		"RABBITMQ_URL":  "amqp://guest:guest@127.0.0.1:1/",
	}
}

func TestRunMissing(t *testing.T) {
	setEnv(t, nil)
	assert.EqualError(t, run(context.Background()), "не заданы DATABASE_URL, S3_ENDPOINT, S3_ACCESS_KEY, S3_SECRET_KEY, RABBITMQ_URL")
}

func TestRunBadAddress(t *testing.T) {
	env := full()
	env["HTTP_ADDRESS"] = ":-1"
	setEnv(t, env)
	assert.ErrorContains(t, run(context.Background()), "HTTP_ADDRESS: ", "ожидалась ошибка прослушивания")
}

func TestRunServesAndStops(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctr, err := tcrabbit.Run(context.Background(), "rabbitmq:4.4.0-management-alpine")
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err, "контейнер RabbitMQ не запущен")
	env := full()
	env["RABBITMQ_URL"], err = ctr.AmqpURL(context.Background())
	require.NoError(t, err)
	env["HTTP_ADDRESS"] = "127.0.0.1:18080"
	setEnv(t, env)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	require.Eventually(t, func() bool {
		resp, err := http.Get("http://127.0.0.1:18080/health")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusServiceUnavailable
	}, 10*time.Second, 50*time.Millisecond, "сервер не ответил")
	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err, "остановка с ошибкой")
	case <-time.After(15 * time.Second):
		require.Fail(t, "сервер не остановился")
	}
}

func TestMainFails(t *testing.T) {
	if os.Getenv("RUN_MAIN") == "1" {
		main()
		return
	}
	setEnv(t, nil)
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainFails$")
	cmd.Env = append(os.Environ(), "RUN_MAIN=1")
	out, err := cmd.CombinedOutput()
	require.Error(t, err, "программа не упала")
	assert.Contains(t, string(out), "Ошибка: не заданы DATABASE_URL")
}
