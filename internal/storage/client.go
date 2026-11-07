// Package storage кладёт и читает файлы аватарок в хранилище по протоколу S3.
// Каждый запрос идёт одной попыткой и попадает в трейсы, кроме проверки Ping.
// После 6 сбоев соединения подряд запросы, кроме Ping, 60 с отклоняются сразу.
// Отмена запроса вызывающим сбоем не считается.
package storage

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go/middleware"
	"github.com/sony/gobreaker/v2"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const region = "us-east-1"

// Client работает с объектами файлов в хранилище.
type Client struct {
	s3     *s3.Client
	bucket string
	upload time.Duration
}

// New собирает клиент бакета bucket хранилища по адресу endpoint. Запросы
// идут через client и подписываются ключом доступа key и секретом secret.
func New(client *http.Client, endpoint, key, secret, bucket string) *Client {
	traced, cb, next := *client, gobreaker.NewTwoStepCircuitBreaker[struct{}](gobreaker.Settings{IsExcluded: canceled}), cmp.Or[http.RoundTripper](client.Transport, http.DefaultTransport)
	traced.Transport = otelhttp.NewTransport(roundTrip(func(r *http.Request) (res *http.Response, err error) {
		if middleware.GetOperationName(r.Context()) == "HeadBucket" {
			return next.RoundTrip(r)
		}
		done, err := cb.Allow()
		if err == nil {
			res, err = next.RoundTrip(r)
			done(err)
		}
		return res, err
	}),
		otelhttp.WithFilter(func(r *http.Request) bool { return middleware.GetOperationName(r.Context()) != "HeadBucket" }),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return "S3." + middleware.GetOperationName(r.Context()) }))
	return &Client{s3.New(s3.Options{
		BaseEndpoint: aws.String(strings.TrimSuffix(endpoint, "/")), UsePathStyle: true, Region: region,
		HTTPClient: &traced, Retryer: aws.NopRetryer{},
		Credentials: credentials.NewStaticCredentialsProvider(key, secret, ""),
	}), bucket, 30 * time.Second}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func canceled(err error) bool { return errors.Is(err, context.Canceled) }
