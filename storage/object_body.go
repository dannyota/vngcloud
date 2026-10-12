package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// objectBody never retains raw reader errors or prints network body internals.
type objectBody struct {
	ctx       context.Context
	body      io.ReadCloser
	cancel    context.CancelFunc
	length    int64
	limit     int64
	read      int64
	terminal  error
	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error
	stop      func() bool
}

func newObjectBody(ctx context.Context, body io.ReadCloser, cancel context.CancelFunc, length, limit int64) *objectBody {
	b := &objectBody{ctx: ctx, body: body, cancel: cancel, length: length, limit: limit}
	b.stop = context.AfterFunc(ctx, func() { b.closeUnderlying() })
	return b
}

func (b *objectBody) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("storage.objectBody{[redacted]}"))
}
func (*objectBody) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }

func (b *objectBody) closeUnderlying() { b.closeOnce.Do(func() { b.closeErr = b.body.Close() }) }
func (b *objectBody) finish(err error) error {
	b.terminal = err
	b.stop()
	b.closeUnderlying()
	b.cancel()
	return err
}
func (b *objectBody) failure(code string, cause error) error {
	if code != "IncompleteObject" && !errors.Is(cause, context.Canceled) && !errors.Is(cause, context.DeadlineExceeded) {
		cause = nil
	}
	return b.finish(objectAPIError("storage.GetObject", 200, code, cause))
}
func (b *objectBody) Read(p []byte) (int, error) {
	if b.closed.Load() {
		return 0, objectAPIError("storage.GetObject", 200, "ObjectBodyClosed", nil)
	}
	if b.terminal != nil {
		return 0, b.terminal
	}
	if b.ctx.Err() != nil {
		return 0, b.failure("RequestFailed", b.ctx.Err())
	}
	if len(p) == 0 {
		return 0, nil
	}
	remaining := b.limit - b.read
	if b.length >= 0 {
		remaining = min(remaining, b.length-b.read)
	}
	if remaining == 0 {
		var probe [1]byte
		n, err := b.body.Read(probe[:])
		if b.ctx.Err() != nil {
			return 0, b.failure("RequestFailed", b.ctx.Err())
		}
		if n != 0 {
			return 0, b.failure("ObjectTooLarge", nil)
		}
		if errors.Is(err, io.EOF) {
			return 0, b.finish(io.EOF)
		}
		if err != nil {
			return 0, b.failure("RequestFailed", err)
		}
		return 0, b.failure("RequestFailed", nil)
	}
	p = p[:min(int64(len(p)), remaining)]
	n, err := b.body.Read(p)
	if n < 0 || n > len(p) {
		return 0, b.failure("RequestFailed", nil)
	}
	b.read += int64(n)
	if b.ctx.Err() != nil {
		return n, b.failure("RequestFailed", b.ctx.Err())
	}
	if errors.Is(err, io.EOF) {
		if b.length >= 0 && b.read < b.length {
			return n, b.failure("IncompleteObject", io.ErrUnexpectedEOF)
		}
		return n, b.finish(io.EOF)
	}
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) && b.length >= 0 && b.read < b.length {
			return n, b.failure("IncompleteObject", io.ErrUnexpectedEOF)
		}
		return n, b.failure("RequestFailed", err)
	}
	return n, nil
}

func (b *objectBody) Close() error {
	b.closed.Store(true)
	b.stop()
	b.closeUnderlying()
	b.cancel()
	if b.closeErr != nil {
		return objectAPIError("storage.GetObject", 200, "RequestFailed", objectContextCause(b.closeErr))
	}
	return nil
}

func objectContextCause(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return nil
}
