package storage

import (
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const bucket = "avatars"

// Upload кладёт data объектом key.
func (c *Client) Upload(ctx context.Context, key string, data []byte) error {
	_, err := c.s3.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key),
		Body: bytes.NewReader(data)})
	return err
}

// Download читает объект key не длиннее limit байт.
func (c *Client) Download(ctx context.Context, key string, limit int64) ([]byte, error) {
	out, err := c.s3.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = out.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(out.Body, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = errors.New("объект файла больше допустимого размера")
	}
	return data, err
}
