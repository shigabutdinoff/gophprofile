// Package storage кладёт и читает файлы аватарок в хранилище по протоколу S3.
// Каждый запрос идёт одной попыткой и попадает в трейсы, кроме проверки Ping.
package storage

import (
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const region = "us-east-1"

// Client работает с объектами файлов в хранилище.
type Client struct {
	s3 *s3.Client
}

// New собирает клиент хранилища по адресу endpoint. Запросы идут через
// client и подписываются ключом доступа key и секретом secret.
func New(client *http.Client, endpoint, key, secret string) *Client {
	traced := *client
	traced.Transport = otelhttp.NewTransport(client.Transport,
		otelhttp.WithFilter(func(r *http.Request) bool { return middleware.GetOperationName(r.Context()) != "HeadBucket" }),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return "S3." + middleware.GetOperationName(r.Context()) }))
	return &Client{s3.New(s3.Options{
		BaseEndpoint: aws.String(strings.TrimSuffix(endpoint, "/")), UsePathStyle: true, Region: region,
		HTTPClient: &traced, Retryer: aws.NopRetryer{},
		Credentials: credentials.NewStaticCredentialsProvider(key, secret, ""),
	})}
}
