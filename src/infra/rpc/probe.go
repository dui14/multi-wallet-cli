package rpc

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Probe behaviour constants.
const (
	probeTimeout    = 10 * time.Second
	maxLagBlocks    = 5
	rateLimitStatus = 429
)

// ProbeResult is the outcome of probing one RPC endpoint.
type ProbeResult struct {
	URL          string
	Latency      time.Duration
	ChainOK      bool
	ChainID      string
	BehindBlocks int64
	RateLimited  bool
	Err          error
}

// Prober checks RPC endpoints for chain identity, latency, lag, and limits.
type Prober struct {
	client *http.Client
	now    func() time.Time
}

// NewProber builds a Prober over client, defaulting to a shared client.
func NewProber(client *http.Client) *Prober {
	if client == nil {
		client = http.DefaultClient
	}
	return &Prober{client: client, now: time.Now}
}

// ProbeEVM checks chain id, latest block number, and rate limiting.
func (p *Prober) ProbeEVM(ctx context.Context, rawURL string, wantChainID int64, tipBlock uint64) ProbeResult {
	start := p.now()
	chainID, chainErr := p.callChainID(ctx, rawURL)
	result := ProbeResult{URL: rawURL, Latency: p.now().Sub(start)}
	if chainErr != nil {
		result.Err = chainErr
		result.RateLimited = isRateLimited(chainErr)
		return result
	}
	result.ChainID = chainIDText(chainID)
	result.ChainOK = chainID == wantChainID
	if !result.ChainOK {
		result.Err = fmt.Errorf("rpc chain mismatch: want %d got %s", wantChainID, maskEndpoint(rawURL))
		return result
	}
	block, blockErr := p.callBlockNumber(ctx, rawURL)
	if blockErr != nil {
		result.Err = blockErr
		result.RateLimited = isRateLimited(blockErr)
		return result
	}
	if tipBlock > block {
		result.BehindBlocks = int64(tipBlock - block)
	}
	if result.BehindBlocks > maxLagBlocks {
		result.Err = fmt.Errorf("rpc %s lags %d blocks", maskEndpoint(rawURL), result.BehindBlocks)
	}
	return result
}

// ProbeSolana checks identity via getHealth plus slot lag.
func (p *Prober) ProbeSolana(ctx context.Context, rawURL string, tipSlot uint64) ProbeResult {
	start := p.now()
	healthy, healthErr := p.callSolanaHealth(ctx, rawURL)
	result := ProbeResult{URL: rawURL, Latency: p.now().Sub(start), ChainOK: healthy}
	if healthErr != nil {
		result.Err = healthErr
		result.RateLimited = isRateLimited(healthErr)
		return result
	}
	if !healthy {
		result.Err = fmt.Errorf("rpc %s is not healthy", maskEndpoint(rawURL))
		return result
	}
	slot, slotErr := p.callSolanaSlot(ctx, rawURL)
	if slotErr != nil {
		result.Err = slotErr
		result.RateLimited = isRateLimited(slotErr)
		return result
	}
	if tipSlot > slot {
		result.BehindBlocks = int64(tipSlot - slot)
	}
	if result.BehindBlocks > maxLagBlocks {
		result.Err = fmt.Errorf("rpc %s lags %d slots", maskEndpoint(rawURL), result.BehindBlocks)
	}
	return result
}
