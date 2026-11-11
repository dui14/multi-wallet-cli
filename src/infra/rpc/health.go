package rpc

import (
	"sync"
	"time"
)

// Health behaviour constants shared by scoring and circuit breaking.
const (
	maxScore           = 100.0
	minScore           = 0.0
	latencyFullScoreMs = 150.0
	latencyZeroScoreMs = 2000.0
	lagPenaltyPerBlock = 2.0
	rateLimitPenalty   = 25.0
	generalPenalty     = 15.0
	successReward      = 5.0
	openThreshold      = 3
	halfOpenAfter      = 30 * time.Second
	halfOpenAllow      = 1
	staleAfter         = 5 * time.Minute
	breakerPenalty     = 40.0
)

// BreakerState is the circuit breaker position for one endpoint.
type BreakerState int

// Circuit breaker positions.
const (
	BreakerClosed BreakerState = iota
	BreakerOpen
	BreakerHalfOpen
)

// Health tracks latency, chain lag, failures, and circuit state per endpoint.
type Health struct {
	LatencyMs       float64
	BehindBlocks    int64
	RateLimited     bool
	ChainMismatch   bool
	LastCheck       time.Time
	Score           float64
	Breaker         BreakerState
	Failures        int
	HalfOpenUsed    int
	halfOpenStarted time.Time
}

// Tracker keeps health records for a set of endpoints.
type Tracker struct {
	mu      sync.Mutex
	records map[string]*Health
	now     func() time.Time
}

// NewTracker builds an empty health tracker.
func NewTracker() *Tracker {
	return &Tracker{records: map[string]*Health{}, now: time.Now}
}

// Observe records a probe outcome for rawURL and returns its updated record.
func (t *Tracker) Observe(rawURL string, latency time.Duration, behindBlocks int64, rateLimited bool, chainMismatch bool, err error) Health {
	t.mu.Lock()
	defer t.mu.Unlock()
	record := t.recordLocked(rawURL)
	now := t.now()
	record.LastCheck = now
	if latency >= 0 {
		record.LatencyMs = float64(latency) / float64(time.Millisecond)
	}
	record.BehindBlocks = behindBlocks
	record.RateLimited = rateLimited
	record.ChainMismatch = chainMismatch
	switch {
	case chainMismatch:
		record.Score = minScore
		t.tripLocked(record, now)
	case err != nil || rateLimited:
		record.Failures++
		penalty := generalPenalty
		if rateLimited {
			penalty = rateLimitPenalty
		}
		record.Score -= penalty
		if record.Score < minScore {
			record.Score = minScore
		}
		if record.Failures >= openThreshold {
			t.tripLocked(record, now)
		}
	default:
		record.Failures = 0
		record.Breaker = BreakerClosed
		record.HalfOpenUsed = 0
		record.Score += successReward
		if record.Score > maxScore {
			record.Score = maxScore
		}
	}
	return *record
}

// Best returns the highest scoring allowed endpoint URL.
func (t *Tracker) Best(candidates []Endpoint) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	bestURL := ""
	bestScore := -1.0
	for _, candidate := range candidates {
		record := t.recordLocked(candidate.URL)
		t.refreshBreakerLocked(record, now)
		if record.Breaker == BreakerOpen {
			continue
		}
		if now.Sub(record.LastCheck) > staleAfter && !record.LastCheck.IsZero() {
			continue
		}
		score := t.effectiveLocked(record)
		if score > bestScore {
			bestScore = score
			bestURL = candidate.URL
		}
	}
	return bestURL, bestURL != ""
}

// Allow checks whether rawURL may currently serve traffic.
func (t *Tracker) Allow(rawURL string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	record := t.recordLocked(rawURL)
	t.refreshBreakerLocked(record, t.now())
	return record.Breaker != BreakerOpen
}

// Snapshot returns a copy of the record for rawURL.
func (t *Tracker) Snapshot(rawURL string) Health {
	t.mu.Lock()
	defer t.mu.Unlock()
	return *t.recordLocked(rawURL)
}

func (t *Tracker) recordLocked(rawURL string) *Health {
	record, ok := t.records[rawURL]
	if !ok {
		record = &Health{Score: maxScore}
		t.records[rawURL] = record
	}
	return record
}

func (t *Tracker) tripLocked(record *Health, now time.Time) {
	record.Breaker = BreakerOpen
	record.HalfOpenUsed = 0
	record.halfOpenStarted = now
}

func (t *Tracker) refreshBreakerLocked(record *Health, now time.Time) {
	if record.Breaker != BreakerOpen {
		return
	}
	if now.Sub(record.halfOpenStarted) < halfOpenAfter {
		return
	}
	record.Breaker = BreakerHalfOpen
	record.HalfOpenUsed = 0
	record.halfOpenStarted = now
}

func (t *Tracker) effectiveLocked(record *Health) float64 {
	score := record.Score + latencyComponent(record.LatencyMs) + lagComponent(record.BehindBlocks)
	if record.Breaker == BreakerOpen {
		score -= breakerPenalty
	}
	if record.RateLimited {
		score -= rateLimitPenalty / 2
	}
	if score > maxScore {
		score = maxScore
	}
	if score < minScore {
		score = minScore
	}
	return score
}

func latencyComponent(latencyMs float64) float64 {
	if latencyMs <= 0 {
		return 0
	}
	if latencyMs <= latencyFullScoreMs {
		return 0
	}
	if latencyMs >= latencyZeroScoreMs {
		return -30
	}
	return -30 * (latencyMs - latencyFullScoreMs) / (latencyZeroScoreMs - latencyFullScoreMs)
}

func lagComponent(behindBlocks int64) float64 {
	if behindBlocks <= 0 {
		return 0
	}
	penalty := float64(behindBlocks) * lagPenaltyPerBlock
	if penalty > 30 {
		penalty = 30
	}
	return -penalty
}
