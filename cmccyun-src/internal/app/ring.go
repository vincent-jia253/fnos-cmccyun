package app

import (
	"strings"
	"sync"
)

// Ring is an io.Writer that keeps the last N log lines in memory and mirrors
// everything to the configured sinks (stdout + log file).
type Ring struct {
	mu      sync.Mutex
	lines   []string
	max     int
	partial string
	sinks   []interface{ Write([]byte) (int, error) }
}

// NewRing creates a buffer holding up to max lines.
func NewRing(max int) *Ring {
	if max <= 0 {
		max = 500
	}
	return &Ring{max: max}
}

// Attach registers mirror writers.
func (r *Ring) Attach(writers []interface{ Write([]byte) (int, error) }) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sinks = writers
}

// Write implements io.Writer.
func (r *Ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	r.partial += string(p)
	for {
		idx := strings.IndexByte(r.partial, '\n')
		if idx < 0 {
			break
		}
		line := r.partial[:idx]
		r.partial = r.partial[idx+1:]
		r.lines = append(r.lines, line)
		if len(r.lines) > r.max {
			r.lines = r.lines[len(r.lines)-r.max:]
		}
	}
	sinks := r.sinks
	r.mu.Unlock()

	for _, s := range sinks {
		_, _ = s.Write(p)
	}
	return len(p), nil
}

// Tail returns the last n lines.
func (r *Ring) Tail(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n <= 0 || n > len(r.lines) {
		n = len(r.lines)
	}
	out := make([]string, n)
	copy(out, r.lines[len(r.lines)-n:])
	return out
}
