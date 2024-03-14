package util

import (
	"context"
	"time"
)

// SleepWithContext pauses the current goroutine until the context expires or
// until after duration d, which ever happens first.
func SleepWithContext(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// CancelableContext utility wraps a context that can be canceled
type CancelableContext struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// Done returns a channel that is closed when this or any parent context is
// canceled
func (ctx *CancelableContext) Done() <-chan struct{} {
	return ctx.ctx.Done()
}

// Cancel this context
func (ctx *CancelableContext) Cancel() {
	ctx.cancel()
}

func NewCancelableContext() CancelableContext {
	return newCancelableContext(context.Background())
}

func NewCancelableContextChild(ctx CancelableContext) CancelableContext {
	return newCancelableContext(ctx.ctx)
}

func newCancelableContext(ctx context.Context) CancelableContext {
	ctx, cancel := context.WithCancel(ctx)
	return CancelableContext{
		ctx:    ctx,
		cancel: cancel,
	}
}
