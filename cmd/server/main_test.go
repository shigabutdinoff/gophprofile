package main

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setEnv(t *testing.T, values map[string]string) {
	t.Helper()
	for _, key := range []string{"DATABASE_URL", "S3_ENDPOINT", "S3_ACCESS_KEY", "S3_SECRET_KEY", "HTTP_ADDRESS"} {
		t.Setenv(key, values[key])
	}
}

func full() map[string]string {
	return map[string]string{
		"DATABASE_URL":  "postgres://postgres:секрет-базы@127.0.0.1:1/postgres",
		"S3_ENDPOINT":   "http://127.0.0.1:1",
		"S3_ACCESS_KEY": "minio",
		"S3_SECRET_KEY": "секрет",
		"HTTP_ADDRESS":  ":-1",
	}
}

func TestRunMissing(t *testing.T) {
	noDatabase := full()
	delete(noDatabase, "DATABASE_URL")
	for _, c := range []struct {
		env  map[string]string
		want string
	}{
		{noDatabase, "не заданы DATABASE_URL"},
		{nil, "не заданы DATABASE_URL, S3_ENDPOINT, S3_ACCESS_KEY, S3_SECRET_KEY"},
	} {
		setEnv(t, c.env)
		assert.EqualError(t, run(), c.want)
	}
}

func TestRunBadDatabase(t *testing.T) {
	env := full()
	env["DATABASE_URL"] = "postgres://postgres:секрет-базы@127.0.0.1:99999/postgres"
	setEnv(t, env)
	err := run()
	require.ErrorContains(t, err, "DATABASE_URL")
	assert.NotContains(t, err.Error(), "секрет-базы", "в ошибке пароль базы")
}

func TestRunBadAddress(t *testing.T) {
	setEnv(t, full())
	assert.ErrorContains(t, run(), "HTTP_ADDRESS: ", "ожидалась ошибка прослушивания")
}

func TestRunBadStorage(t *testing.T) {
	for _, endpoint := range []string{"minio:9000", "http://", "::"} {
		env := full()
		env["S3_ENDPOINT"] = endpoint
		setEnv(t, env)
		assert.ErrorContains(t, run(), "S3_ENDPOINT", "адрес %q", endpoint)
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
