package timing

import (
	"sort"
	"sync"
	"time"
)

// latencyWindow keeps the most recent samples for one measurement series.
const latencyWindow = 256

// Latency records round-trip durations and reports percentile summaries.
type Latency struct {
	mu      sync.Mutex
	samples []time.Duration
}

// NewLatency builds an empty recorder.
func NewLatency() *Latency {
	return &Latency{samples: make([]time.Duration, 0, latencyWindow)}
}

// Observe records one sample, evicting the oldest beyond the window.
func (l *Latency) Observe(sample time.Duration) {
	if sample < 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.samples) == latencyWindow {
		copy(l.samples, l.samples[1:])
		l.samples[len(l.samples)-1] = sample
		return
	}
	l.samples = append(l.samples, sample)
}

// Start returns a function that records the elapsed time when called.
func (l *Latency) Start() func() {
	start := time.Now()
	return func() { l.Observe(time.Since(start)) }
}

// Count returns how many samples are stored.
func (l *Latency) Count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.samples)
}

// Reset clears all recorded samples.
func (l *Latency) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.samples = l.samples[:0]
}

// Summary reports count, min, max, average, and p50/p95/p99 over samples.
// Zero counts report zero durations.
func (l *Latency) Summary() LatencySummary {
	l.mu.Lock()
	defer l.mu.Unlock()
	summary := LatencySummary{Count: len(l.samples)}
	if len(l.samples) == 0 {
		return summary
	}
	sorted := make([]time.Duration, len(l.samples))
	copy(sorted, l.samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	summary.Min = sorted[0]
	summary.Max = sorted[len(sorted)-1]
	var total time.Duration
	for _, sample := range sorted {
		total += sample
	}
	summary.Average = total / time.Duration(len(sorted))
	summary.P50 = percentile(sorted, 50)
	summary.P95 = percentile(sorted, 95)
	summary.P99 = percentile(sorted, 99)
	return summary
}

// LatencySummary is the aggregated view of one measurement series.
type LatencySummary struct {
	Count   int
	Min     time.Duration
	Max     time.Duration
	Average time.Duration
	P50     time.Duration
	P95     time.Duration
	P99     time.Duration
}

func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := (p*len(sorted)+99)/100 - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}
