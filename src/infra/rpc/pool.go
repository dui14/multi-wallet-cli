package rpc

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"multi-wallet-cli/infra/logging"
)

// ErrNotReady means the endpoint answered but the result is not available yet
// (for example a pending transaction receipt). Pool.Do tries the next endpoint
// without parking this one in cooldown.
var ErrNotReady = errors.New("rpc result not ready")

// Tier ranks RPC endpoint sources from most to least trusted.
type Tier int

// Endpoint source tiers in selection order.
const (
	TierPrivate    Tier = 1
	TierDiscovered Tier = 2
	TierSeed       Tier = 3
)

// Pool behaviour constants shared by selection and retry.
const (
	cooldownPeriod = 60 * time.Second
)

// httpEndpointSchemes lists the URL schemes the shared JSON-RPC transport can
// call. WebSocket endpoints are accepted in configuration but skipped when
// selecting an endpoint for JSON-RPC.
var httpEndpointSchemes = map[string]bool{
	"http":  true,
	"https": true,
}

// Endpoint is one RPC URL together with its trust tier.
type Endpoint struct {
	URL  string
	Tier Tier
}

// Pool selects RPC endpoints per chain across three tiers with round-robin,
// temporary cooldown for failed endpoints, and optional private-only lock.
type Pool struct {
	chain       string
	privateOnly bool
	mu          sync.Mutex
	tiers       map[Tier][]Endpoint
	cursor      map[Tier]int
	cooledUntil map[string]time.Time
	skipped     []string
	now         func() time.Time
}

// NewPool builds a Pool for chain from the three tier URL lists.
func NewPool(chain string, privateOnly bool, privateURLs []string, discoveredURLs []string, seedURLs []string) *Pool {
	return &Pool{
		chain:       chain,
		privateOnly: privateOnly,
		tiers: map[Tier][]Endpoint{
			TierPrivate:    buildEndpoints(privateURLs, TierPrivate),
			TierDiscovered: buildEndpoints(discoveredURLs, TierDiscovered),
			TierSeed:       buildEndpoints(seedURLs, TierSeed),
		},
		cursor:      map[Tier]int{},
		cooledUntil: map[string]time.Time{},
		skipped:     skippedEndpoints(privateURLs, discoveredURLs, seedURLs),
		now:         time.Now,
	}
}

// NewSeedPool builds a Pool over one chain's seed endpoints only.
func NewSeedPool(chain string, seedURLs []string) *Pool {
	return NewPool(chain, false, nil, nil, seedURLs)
}

// SetPrivateOnly switches strict private-only mode for the chain.
func (p *Pool) SetPrivateOnly(privateOnly bool) {
	p.mu.Lock()
	p.privateOnly = privateOnly
	p.mu.Unlock()
}

// Select returns the next healthy endpoint URL in tier priority order.
func (p *Pool) Select() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	endpoint, ok := p.selectLocked()
	if !ok {
		return "", p.exhaustedLocked()
	}
	p.cursor[endpoint.Tier]++
	return endpoint.URL, nil
}

// ReportFailure parks url in cooldown for cooldownPeriod.
func (p *Pool) ReportFailure(rawURL string) {
	p.mu.Lock()
	p.cooledUntil[rawURL] = p.now().Add(cooldownPeriod)
	p.mu.Unlock()
}

// ReportSuccess clears any cooldown recorded for url.
func (p *Pool) ReportSuccess(rawURL string) {
	p.mu.Lock()
	delete(p.cooledUntil, rawURL)
	p.mu.Unlock()
}

// Do runs fn against endpoints in tier order, rotating through every healthy
// endpoint of a tier before dropping to the next one. Endpoints that fail are
// parked in cooldown so later calls rotate past them. Private-only pools never
// leave tier 1.
func (p *Pool) Do(ctx context.Context, fn func(context.Context, string) error) error {
	candidates := p.snapshot()
	p.mu.Lock()
	skipped := append([]string(nil), p.skipped...)
	p.mu.Unlock()
	total := 0
	for _, tier := range p.orderedTiers() {
		total += len(candidates[tier])
	}
	if total == 0 {
		return p.exhaustedLocked()
	}
	var tried []string
	notReady := 0
	for _, tier := range p.orderedTiers() {
		for _, endpoint := range candidates[tier] {
			if err := ctx.Err(); err != nil {
				return err
			}
			err := fn(ctx, endpoint.URL)
			if err == nil {
				p.ReportSuccess(endpoint.URL)
				return nil
			}
			if errors.Is(err, ErrNotReady) {
				notReady++
				continue
			}
			p.ReportFailure(endpoint.URL)
			tried = append(tried, maskEndpoint(endpoint.URL))
		}
	}
	if notReady > 0 && len(tried) == 0 {
		return ErrNotReady
	}
	failure := fmt.Errorf("rpc %s: all endpoints failed: %s", p.chain, strings.Join(tried, ", "))
	if len(skipped) > 0 {
		return fmt.Errorf("%w (skipped non-HTTP: %s)", failure, strings.Join(skipped, ", "))
	}
	return failure
}

// LiveURLs returns healthy HTTP endpoints in tier order for parallel reads
// and blast-style broadcasts.
func (p *Pool) LiveURLs() []string {
	if p == nil {
		return nil
	}
	candidates := p.snapshot()
	p.mu.Lock()
	tiers := p.orderedTiers()
	p.mu.Unlock()
	var out []string
	for _, tier := range tiers {
		for _, endpoint := range candidates[tier] {
			out = append(out, endpoint.URL)
		}
	}
	return out
}

// Race runs fn against every healthy endpoint at once and returns on the
// first success without cancelling the remaining sends, so a lying public
// RPC cannot abort broadcast to the sequencer.
func (p *Pool) Race(ctx context.Context, fn func(context.Context, string) error) error {
	urls := p.LiveURLs()
	if len(urls) == 0 {
		return p.exhaustedLocked()
	}
	if len(urls) == 1 {
		return p.Do(ctx, fn)
	}
	type outcome struct {
		url string
		err error
	}
	results := make(chan outcome, len(urls))
	for _, rawURL := range urls {
		rawURL := rawURL
		go func() {
			results <- outcome{url: rawURL, err: fn(ctx, rawURL)}
		}()
	}
	var tried []string
	notReady := 0
	for i := 0; i < len(urls); i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result := <-results:
			if result.err == nil {
				p.ReportSuccess(result.url)
				return nil
			}
			if errors.Is(result.err, ErrNotReady) {
				notReady++
				continue
			}
			p.ReportFailure(result.url)
			tried = append(tried, maskEndpoint(result.url))
		}
	}
	if notReady > 0 && len(tried) == 0 {
		return ErrNotReady
	}
	return fmt.Errorf("rpc %s: all endpoints failed: %s", p.chain, strings.Join(tried, ", "))
}

// OrderedTiers returns tier order honoring private-only mode.
func (p *Pool) OrderedTiers() []Tier {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.orderedTiers()
}

func (p *Pool) orderedTiers() []Tier {
	if p.privateOnly {
		return []Tier{TierPrivate}
	}
	return []Tier{TierPrivate, TierDiscovered, TierSeed}
}

func (p *Pool) selectLocked() (Endpoint, bool) {
	for _, tier := range p.orderedTiers() {
		list := p.tiers[tier]
		for i := 0; i < len(list); i++ {
			endpoint := list[(p.cursor[tier]+i)%len(list)]
			if p.now().Before(p.cooledUntil[endpoint.URL]) {
				continue
			}
			return endpoint, true
		}
	}
	return Endpoint{}, false
}

func (p *Pool) exhaustedLocked() error {
	if p.privateOnly {
		return fmt.Errorf("rpc %s: private_only enabled, no healthy private endpoint", p.chain)
	}
	if len(p.skipped) > 0 {
		return fmt.Errorf("rpc %s: no healthy endpoint across tiers (skipped non-HTTP: %s)", p.chain, strings.Join(p.skipped, ", "))
	}
	return fmt.Errorf("rpc %s: no healthy endpoint across tiers", p.chain)
}

func (p *Pool) snapshot() map[Tier][]Endpoint {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[Tier][]Endpoint, len(p.tiers))
	for _, tier := range p.orderedTiers() {
		var live []Endpoint
		list := p.tiers[tier]
		start := 0
		if len(list) > 0 {
			start = p.cursor[tier] % len(list)
		}
		for i := 0; i < len(list); i++ {
			endpoint := list[(start+i)%len(list)]
			if p.now().Before(p.cooledUntil[endpoint.URL]) {
				continue
			}
			live = append(live, endpoint)
		}
		out[tier] = live
	}
	return out
}

func buildEndpoints(rawURLs []string, tier Tier) []Endpoint {
	seen := map[string]bool{}
	out := make([]Endpoint, 0, len(rawURLs))
	for _, rawURL := range rawURLs {
		trimmed := strings.TrimSpace(rawURL)
		if trimmed == "" || seen[trimmed] || !isHTTPEndpoint(trimmed) {
			continue
		}
		seen[trimmed] = true
		out = append(out, Endpoint{URL: trimmed, Tier: tier})
	}
	return out
}

func skippedEndpoints(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range lists {
		for _, rawURL := range list {
			trimmed := strings.TrimSpace(rawURL)
			if trimmed == "" || seen[trimmed] || isHTTPEndpoint(trimmed) {
				continue
			}
			seen[trimmed] = true
			out = append(out, maskEndpoint(trimmed))
		}
	}
	return out
}

// IsHTTPEndpoint reports whether rawURL can be called over the JSON-RPC HTTP
// transport. WebSocket endpoints return false.
func IsHTTPEndpoint(rawURL string) bool {
	return isHTTPEndpoint(rawURL)
}

func isHTTPEndpoint(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return false
	}
	return httpEndpointSchemes[strings.ToLower(parsed.Scheme)]
}

func maskEndpoint(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return "invalid-endpoint"
	}
	return logging.MaskRPCURL(rawURL)
}
