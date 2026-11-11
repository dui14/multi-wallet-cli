package timing

import (
	"context"
	"sync"
	"time"
)

// Clock reads the current time with a configurable offset so callers can
// skew their view towards a trusted remote source.
type Clock struct {
	mu     sync.Mutex
	now    func() time.Time
	offset time.Duration
}

// NewClock builds a wall clock with zero offset.
func NewClock() *Clock {
	return &Clock{now: time.Now}
}

// NewFixedClock builds a clock pinned to a fixed instant for tests.
func NewFixedClock(fixed time.Time) *Clock {
	return &Clock{now: func() time.Time { return fixed }}
}

// Now returns local time plus the configured offset.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now().Add(c.offset)
}

// Since returns the time elapsed since t under the adjusted clock.
func (c *Clock) Since(t time.Time) time.Duration {
	return c.Now().Sub(t)
}

// Offset returns the currently applied skew.
func (c *Clock) Offset() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.offset
}

// SetOffset replaces the skew applied to local time.
func (c *Clock) SetOffset(offset time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.offset = offset
}

// ObserveRemote records a trusted remote timestamp and skews the local
// clock by the difference so later Now() calls track the remote source.
func (c *Clock) ObserveRemote(remote time.Time) time.Duration {
	c.mu.Lock()
	local := c.now()
	c.offset = remote.Sub(local)
	c.mu.Unlock()
	return c.offset
}

// Sleeper sleeps until a duration elapses or the context is cancelled.
type Sleeper struct {
	sleep func(context.Context, time.Duration) error
}

// NewSleeper builds a context-aware sleeper.
func NewSleeper() *Sleeper {
	return &Sleeper{sleep: sleepWithContext}
}

// NewSleeperWith replaces the sleep function, for tests.
func NewSleeperWith(sleep func(context.Context, time.Duration) error) *Sleeper {
	return &Sleeper{sleep: sleep}
}

// Sleep waits for d or returns the context error first.
func (s *Sleeper) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	return s.sleep(ctx, d)
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WithTimeout derives a context carrying the default per-call RPC timeout
// when the parent has no earlier deadline.
func WithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(parent)
	}
	if deadline, ok := parent.Deadline(); ok && time.Until(deadline) <= timeout {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}

// Backoff returns exponential retry delays capped at max, starting at base
// for attempt 0 with no jitter so behaviour stays deterministic in tests.
func Backoff(base, max time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = 100 * time.Millisecond
	}
	if max <= 0 {
		max = 5 * time.Second
	}
	delay := base
	for i := 0; i < attempt; i++ {
		delay *= 2
		if delay >= max {
			return max
		}
	}
	if delay > max {
		return max
	}
	return delay
}
