package timing

import (
	"testing"
	"time"
)

func TestLatencyStartRecordsElapsed(t *testing.T) {
	latency := NewLatency()
	done := latency.Start()
	time.Sleep(2 * time.Millisecond)
	done()
	summary := latency.Summary()
	if summary.Count != 1 {
		t.Fatalf("expected 1 sample, got %d", summary.Count)
	}
	if summary.Min <= 0 {
		t.Fatalf("expected positive sample, got %v", summary.Min)
	}
}

func TestLatencySummaryPercentiles(t *testing.T) {
	latency := NewLatency()
	for i := 1; i <= 100; i++ {
		latency.Observe(time.Duration(i) * time.Millisecond)
	}
	summary := latency.Summary()
	if summary.Count != 100 {
		t.Fatalf("expected 100, got %d", summary.Count)
	}
	if summary.Min != time.Millisecond || summary.Max != 100*time.Millisecond {
		t.Fatalf("min/max wrong: %+v", summary)
	}
	if summary.P50 != 50*time.Millisecond || summary.P95 != 95*time.Millisecond || summary.P99 != 99*time.Millisecond {
		t.Fatalf("percentiles wrong: %+v", summary)
	}
	if summary.Average != 5050*time.Millisecond/100 {
		t.Fatalf("average wrong: %v", summary.Average)
	}
}

func TestLatencyWindowEvictsOldest(t *testing.T) {
	latency := NewLatency()
	for i := 0; i < latencyWindow+10; i++ {
		latency.Observe(time.Millisecond)
	}
	if latency.Count() != latencyWindow {
		t.Fatalf("expected window %d, got %d", latencyWindow, latency.Count())
	}
}

func TestLatencyRejectsNegativeAndResets(t *testing.T) {
	latency := NewLatency()
	latency.Observe(-time.Second)
	if latency.Count() != 0 {
		t.Fatal("negative sample must be ignored")
	}
	latency.Observe(time.Second)
	latency.Reset()
	if latency.Count() != 0 {
		t.Fatal("reset must clear samples")
	}
	empty := NewLatency().Summary()
	if empty.Count != 0 || empty.P50 != 0 {
		t.Fatalf("empty summary must be zero: %+v", empty)
	}
}
