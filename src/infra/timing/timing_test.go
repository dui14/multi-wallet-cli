package timing

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClockNowAppliesOffset(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := NewFixedClock(base)
	if !clock.Now().Equal(base) {
		t.Fatalf("expected %v, got %v", base, clock.Now())
	}
	clock.SetOffset(5 * time.Second)
	if !clock.Now().Equal(base.Add(5 * time.Second)) {
		t.Fatalf("offset not applied: %v", clock.Now())
	}
	if clock.Offset() != 5*time.Second {
		t.Fatalf("expected offset 5s, got %v", clock.Offset())
	}
	if clock.Since(base) != 5*time.Second {
		t.Fatalf("expected since 5s, got %v", clock.Since(base))
	}
}

func TestClockObserveRemoteSkewsLocal(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := NewFixedClock(base)
	remote := base.Add(-90 * time.Second)
	offset := clock.ObserveRemote(remote)
	if offset != -90*time.Second {
		t.Fatalf("expected -90s offset, got %v", offset)
	}
	if !clock.Now().Equal(remote) {
		t.Fatalf("clock should track remote, got %v", clock.Now())
	}
}

func TestSleeperCompletesAndCancels(t *testing.T) {
	sleeper := NewSleeper()
	if err := sleeper.Sleep(context.Background(), 0); err != nil {
		t.Fatalf("zero sleep must be no-op: %v", err)
	}
	if err := sleeper.Sleep(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleeper.Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestWithTimeoutKeepsEarlierDeadline(t *testing.T) {
	parent, cancelParent := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelParent()
	child, cancelChild := WithTimeout(parent, time.Second)
	defer cancelChild()
	deadline, ok := child.Deadline()
	if !ok {
		t.Fatal("expected deadline")
	}
	if remaining := time.Until(deadline); remaining > 10*time.Millisecond {
		t.Fatalf("must keep earlier parent deadline, got %v", remaining)
	}
	plain, cancelPlain := WithTimeout(context.Background(), 0)
	defer cancelPlain()
	if _, ok := plain.Deadline(); ok {
		t.Fatal("zero timeout must not set a deadline")
	}
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	base := 100 * time.Millisecond
	max := 800 * time.Millisecond
	got := []time.Duration{
		Backoff(base, max, 0),
		Backoff(base, max, 1),
		Backoff(base, max, 2),
		Backoff(base, max, 3),
		Backoff(base, max, 4),
	}
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond, 800 * time.Millisecond}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt %d: want %v got %v", i, want[i], got[i])
		}
	}
}
