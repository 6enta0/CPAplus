package executor

import (
	"context"
	"time"
)

type downstreamWebsocketContextKey struct{}
type openAICompatBootstrapTimeoutContextKey struct{}

// WithDownstreamWebsocket marks the current request as coming from a downstream websocket connection.
func WithDownstreamWebsocket(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, downstreamWebsocketContextKey{}, true)
}

// DownstreamWebsocket reports whether the current request originates from a downstream websocket connection.
func DownstreamWebsocket(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	raw := ctx.Value(downstreamWebsocketContextKey{})
	enabled, ok := raw.(bool)
	return ok && enabled
}

// WithOpenAICompatBootstrapTimeout stores the compat bootstrap timeout for the current request attempt.
func WithOpenAICompatBootstrapTimeout(ctx context.Context, timeout time.Duration) context.Context {
	if timeout <= 0 {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, openAICompatBootstrapTimeoutContextKey{}, timeout)
}

// OpenAICompatBootstrapTimeout returns the compat bootstrap timeout for the current request attempt.
func OpenAICompatBootstrapTimeout(ctx context.Context) time.Duration {
	if ctx == nil {
		return 0
	}
	raw := ctx.Value(openAICompatBootstrapTimeoutContextKey{})
	timeout, ok := raw.(time.Duration)
	if !ok || timeout <= 0 {
		return 0
	}
	return timeout
}
