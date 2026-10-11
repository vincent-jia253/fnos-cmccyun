package api

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
	"time"
)

// retryDo runs fn until it succeeds, up to attempts times, with a backoff.
// It is used for the write paths where a transient network error would
// otherwise make the whole transfer fail (borrowed from OpenList's 139 driver,
// which retries each uploaded part three times).
//
// Permanent failures (auth problems, cancelled contexts, protocol errors the
// service answered on purpose) are returned immediately.
func retryDo(ctx context.Context, attempts int, fn func() error) error {
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return err
		}
		if !retryable(err) || i == attempts-1 {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(backoffDelay(i + 1)):
		}
	}
	return err
}

// backoffDelay returns the wait before retry number n (1-based): 500ms, 1s,
// 2s, 4s, then capped at 5s.
func backoffDelay(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	d := 500 * time.Millisecond
	for i := 1; i < n; i++ {
		d *= 2
		if d >= 5*time.Second {
			return 5 * time.Second
		}
	}
	return d
}

// retryable reports whether an error is worth trying again.
func retryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, ErrNoAuth) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ETIMEDOUT) {
		return true
	}
	msg := err.Error()
	// Transient conditions that arrive as plain strings (net/http does not
	// always wrap them): truncated bodies and reset sockets.
	for _, s := range []string{
		"unexpected EOF", "connection reset by peer", "broken pipe",
		"i/o timeout", "temporary failure", "connection refused",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	// HTTP statuses that a retry may fix: 5xx and 429.
	for _, s := range []string{"HTTP 500", "HTTP 502", "HTTP 503", "HTTP 504", "HTTP 429"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
