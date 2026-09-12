package network

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/gregfurman/micropython-go/internal/host/abi"
)

// prepareIO interrupts synchronous I/O without retaining guest buffers in a
// worker goroutine. Listeners without SetDeadline are closed on cancellation.
// The returned function joins a racing cancellation before the next operation
// and reports a close that failed.
func prepareIO(ctx context.Context, resource io.Closer) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	deadline, hasDeadline := resource.(interface{ SetDeadline(time.Time) error })
	if hasDeadline {
		when, _ := ctx.Deadline()
		if err := deadline.SetDeadline(when); err != nil {
			return nil, err
		}
	}

	done := make(chan error, 1)
	stop := context.AfterFunc(ctx, func() {
		defer close(done)

		if hasDeadline {
			if err := deadline.SetDeadline(time.Now()); err == nil {
				return
			}
		}

		done <- resource.Close()
	})

	return func() error {
		if !stop() {
			return <-done
		}

		return nil
	}, nil
}

func ioResult(ctx context.Context, count, capacity int, err error) int32 {
	if count < 0 || count > capacity {
		return -abi.EIO
	}

	if count > 0 {
		return int32(count)
	}

	if ctx.Err() != nil {
		return errnoOf(ctx.Err())
	}

	if errors.Is(err, io.EOF) {
		return STATUS_OK
	}

	return errnoOf(err)
}
