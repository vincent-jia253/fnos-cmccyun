package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
	"time"
)

func TestRetryDoRecoversFromTransient(t *testing.T) {
	attempts := 0
	err := retryDo(context.Background(), 3, func() error {
		attempts++
		if attempts < 3 {
			return fmt.Errorf("read: %w", syscall.ECONNRESET)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestRetryDoStopsOnPermanentError(t *testing.T) {
	attempts := 0
	want := errors.New("Authorization 格式不正确")
	err := retryDo(context.Background(), 5, func() error {
		attempts++
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
	if attempts != 1 {
		t.Fatalf("permanent error must not be retried, attempts=%d", attempts)
	}
}

func TestRetryDoHonoursContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	err := retryDo(ctx, 5, func() error {
		attempts++
		cancel()
		return &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if attempts != 1 {
		t.Fatalf("cancelled context must not retry, attempts=%d", attempts)
	}
}

func TestRetryable(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{syscall.ECONNRESET, true},
		{syscall.EPIPE, true},
		{syscall.ETIMEDOUT, true},
		{&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, true},
		{fmt.Errorf("unexpected EOF"), true},
		{errors.New("Account or password error"), false},
		{errors.New("file not found"), false},
	}
	for _, c := range cases {
		if got := retryable(c.err); got != c.want {
			t.Errorf("retryable(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

// TestRetryBackoffIsBounded guards the sleep helper against runaway waits.
func TestRetryBackoffIsBounded(t *testing.T) {
	if d := backoffDelay(1); d != 500*time.Millisecond {
		t.Fatalf("first backoff = %v", d)
	}
	if d := backoffDelay(10); d != 5*time.Second {
		t.Fatalf("backoff must cap at 5s, got %v", d)
	}
}
