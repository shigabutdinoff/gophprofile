// Package storage кладёт и читает файлы аватарок в хранилище по протоколу S3.
// Каждый запрос идёт одной попыткой.
package storage

import (
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const region = "us-east-1"

// Client работает с объектами файлов в хранилище.
type Client struct {
	s3 *s3.Client
}

// New собирает клиент хранилища по адресу endpoint. Запросы идут через
// client и подписываются ключом доступа key и секретом secret.
func New(client *http.Client, endpoint, key, secret string) *Client {
	return &Client{s3.New(s3.Options{
		BaseEndpoint: aws.String(strings.TrimSuffix(endpoint, "/")), UsePathStyle: true, Region: region,
		HTTPClient: client, Retryer: aws.NopRetryer{},
		Credentials: credentials.NewStaticCredentialsProvider(key, secret, ""),
	})}
}
