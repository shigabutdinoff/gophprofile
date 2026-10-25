package worker

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

func lost(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if pg, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgerrcode.IsConnectionException(pg.Code) || pg.Code == pgerrcode.AdminShutdown || pg.Code == pgerrcode.CannotConnectNow
	}
	if status, ok := errors.AsType[interface {
		error
		HTTPStatusCode() int
	}](err); ok && status.HTTPStatusCode() >= 500 {
		return true
	}
	_, connect := errors.AsType[*pgconn.ConnectError](err)
	_, broken := errors.AsType[net.Error](err)
	return connect || broken || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func graceful(ctx context.Context, grace time.Duration) (context.Context, context.CancelFunc) {
	work, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stop := context.AfterFunc(ctx, func() { time.AfterFunc(grace, cancel) })
	return work, func() { stop(); cancel() }
}
