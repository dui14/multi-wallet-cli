package rpc

import (
	"fmt"
	"testing"
	"time"
)

func TestHealthPrefersFastInSyncEndpoint(t *testing.T) {
	tracker := NewTracker()
	slow := "https://slow.example/rpc"
	fast := "https://fast.example/rpc"
	tracker.Observe(slow, 1500*time.Millisecond, 12, false, false, nil)
	tracker.Observe(fast, 80*time.Millisecond, 0, false, false, nil)
	got, ok := tracker.Best([]Endpoint{{URL: slow, Tier: TierSeed}, {URL: fast, Tier: TierSeed}})
	if !ok {
		t.Fatal("expected a best endpoint")
	}
	if got != fast {
		t.Fatalf("expected fast endpoint, got %s", got)
	}
}

func TestHealthOpensBreakerAfterRepeatedFailures(t *testing.T) {
	tracker := NewTracker()
	flaky := "https://flaky.example/rpc"
	steady := "https://steady.example/rpc"
	tracker.Observe(steady, 100*time.Millisecond, 0, false, false, nil)
	for i := 0; i < 3; i++ {
		tracker.Observe(flaky, -1, 0, false, false, fmt.Errorf("boom"))
	}
	if tracker.Allow(flaky) {
		t.Fatal("flaky endpoint must be circuit open")
	}
	got, ok := tracker.Best([]Endpoint{{URL: flaky, Tier: TierSeed}, {URL: steady, Tier: TierSeed}})
	if !ok || got != steady {
		t.Fatalf("expected steady endpoint during open breaker, got %s %v", got, ok)
	}
}

func TestHealthHalfOpenRecovery(t *testing.T) {
	tracker := NewTracker()
	flaky := "https://flaky.example/rpc"
	for i := 0; i < 3; i++ {
		tracker.Observe(flaky, -1, 0, false, false, fmt.Errorf("boom"))
	}
	now := time.Now()
	tracker.now = func() time.Time { return now.Add(31 * time.Second) }
	if !tracker.Allow(flaky) {
		t.Fatal("breaker must move to half-open after cooldown")
	}
	tracker.Observe(flaky, 90*time.Millisecond, 0, false, false, nil)
	if !tracker.Allow(flaky) {
		t.Fatal("endpoint must close breaker after success")
	}
}

func TestHealthChainMismatchScoresZero(t *testing.T) {
	tracker := NewTracker()
	wrong := "https://wrong.example/rpc"
	right := "https://right.example/rpc"
	tracker.Observe(wrong, 50*time.Millisecond, 0, false, true, nil)
	tracker.Observe(right, 900*time.Millisecond, 0, false, false, nil)
	got, ok := tracker.Best([]Endpoint{{URL: wrong, Tier: TierSeed}, {URL: right, Tier: TierSeed}})
	if !ok || got != right {
		t.Fatalf("expected right-chain endpoint, got %s %v", got, ok)
	}
	if snapshot := tracker.Snapshot(wrong); snapshot.Score != 0 {
		t.Fatalf("chain mismatch must score zero, got %v", snapshot.Score)
	}
}

func TestHealthRateLimitPenalty(t *testing.T) {
	tracker := NewTracker()
	limited := "https://limited.example/rpc"
	normal := "https://normal.example/rpc"
	tracker.Observe(limited, 80*time.Millisecond, 0, true, false, nil)
	tracker.Observe(normal, 120*time.Millisecond, 0, false, false, nil)
	got, ok := tracker.Best([]Endpoint{{URL: limited, Tier: TierSeed}, {URL: normal, Tier: TierSeed}})
	if !ok || got != normal {
		t.Fatalf("expected normal endpoint over rate-limited one, got %s %v", got, ok)
	}
}
