package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Discovery cache behaviour.
const (
	discoveryTTL = 24 * time.Hour
)

// Candidate is one discovered RPC endpoint awaiting user accept.
type Candidate struct {
	URL            string `json:"url"`
	LatencyMs      int64  `json:"latencyMs"`
	BlockNumber    uint64 `json:"blockNumber"`
	DiscoveredAt   string `json:"discoveredAt"`
	ExpiresAt      string `json:"expiresAt"`
	AcceptedByUser bool   `json:"acceptedByUser"`
}

// Discovery merges community registry sources with seed endpoints and
// persists accepted candidates with a 24h TTL.
type Discovery struct {
	mu       sync.Mutex
	path     string
	prober   *Prober
	now      func() time.Time
	records  map[string][]Candidate
	registry map[string][]string
}

// NewDiscovery builds a Discovery backed by path for cache persistence.
func NewDiscovery(path string, prober *Prober, registry map[string][]string) *Discovery {
	if prober == nil {
		prober = NewProber(nil)
	}
	return &Discovery{path: path, prober: prober, now: time.Now, records: map[string][]Candidate{}, registry: registry}
}

// Load reads cache from disk, ignoring corrupt or expired content safely.
func (d *Discovery) Load() {
	d.mu.Lock()
	defer d.mu.Unlock()
	data, err := os.ReadFile(d.path)
	if err != nil {
		d.records = map[string][]Candidate{}
		return
	}
	var decoded map[string][]Candidate
	if err := json.Unmarshal(data, &decoded); err != nil {
		d.records = map[string][]Candidate{}
		return
	}
	d.records = d.pruneLocked(decoded, d.now())
}

// Accepted returns live accepted endpoints for chain.
func (d *Discovery) Accepted(chain string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	var out []string
	for _, candidate := range d.records[chain] {
		if !candidate.AcceptedByUser {
			continue
		}
		expiry, err := time.Parse(time.RFC3339, candidate.ExpiresAt)
		if err != nil || !now.Before(expiry) {
			continue
		}
		out = append(out, candidate.URL)
	}
	return out
}

// Accept marks rawURL accepted for chain with fresh TTL and persists.
func (d *Discovery) Accept(chain string, candidate Candidate) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	candidate.DiscoveredAt = now.Format(time.RFC3339)
	candidate.ExpiresAt = now.Add(discoveryTTL).Format(time.RFC3339)
	candidate.AcceptedByUser = true
	list := d.records[chain]
	replaced := false
	for i, item := range list {
		if item.URL == candidate.URL {
			list[i] = candidate
			replaced = true
		}
	}
	if !replaced {
		list = append(list, candidate)
	}
	d.records[chain] = d.pruneLocked(map[string][]Candidate{chain: list}, now)[chain]
	return d.saveLocked()
}

// Find probes registry plus seed URLs and returns healthy candidates.
func (d *Discovery) Find(ctx context.Context, chain string, wantChainID int64, tipBlock uint64, seedURLs ...string) []Candidate {
	sources := append([]string{}, d.registry[chain]...)
	sources = append(sources, seedURLs...)
	seen := map[string]bool{}
	var out []Candidate
	for _, rawURL := range sources {
		trimmed := strings.TrimSpace(rawURL)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		result := d.prober.ProbeEVM(ctx, trimmed, wantChainID, tipBlock)
		if result.Err != nil || !result.ChainOK {
			continue
		}
		now := d.now()
		out = append(out, Candidate{
			URL:            trimmed,
			LatencyMs:      result.Latency.Milliseconds(),
			BlockNumber:    tipBlock,
			DiscoveredAt:   now.Format(time.RFC3339),
			ExpiresAt:      now.Add(discoveryTTL).Format(time.RFC3339),
			AcceptedByUser: false,
		})
	}
	return out
}

func (d *Discovery) pruneLocked(records map[string][]Candidate, now time.Time) map[string][]Candidate {
	out := make(map[string][]Candidate, len(records))
	for chain, list := range records {
		kept := make([]Candidate, 0, len(list))
		for _, candidate := range list {
			if strings.TrimSpace(candidate.URL) == "" {
				continue
			}
			expiry, err := time.Parse(time.RFC3339, candidate.ExpiresAt)
			if err != nil || !now.Before(expiry) {
				continue
			}
			kept = append(kept, candidate)
		}
		if len(kept) > 0 {
			out[chain] = kept
		}
	}
	return out
}

func (d *Discovery) saveLocked() error {
	if d.path == "" {
		return fmt.Errorf("discovery cache path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(d.path), 0o700); err != nil {
		return fmt.Errorf("create discovery cache dir: %w", err)
	}
	data, err := json.MarshalIndent(d.records, "", "  ")
	if err != nil {
		return fmt.Errorf("encode discovery cache: %w", err)
	}
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write discovery cache: %w", err)
	}
	if err := os.Rename(tmp, d.path); err != nil {
		return fmt.Errorf("commit discovery cache: %w", err)
	}
	return nil
}
