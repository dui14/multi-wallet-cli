package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
)

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type hexResult struct {
	Result string    `json:"result"`
	Error  *rpcError `json:"error"`
}

type u64Result struct {
	Result uint64    `json:"result"`
	Error  *rpcError `json:"error"`
}

type strResult struct {
	Result string    `json:"result"`
	Error  *rpcError `json:"error"`
}

// EVMChainID reads the chain id reported by an EVM JSON-RPC endpoint.
func (p *Prober) EVMChainID(ctx context.Context, rawURL string) (int64, error) {
	return p.callChainID(ctx, rawURL)
}

// SolanaGenesisHash reads the genesis hash, the native network identity of a
// Solana cluster, from a Solana JSON-RPC endpoint.
func (p *Prober) SolanaGenesisHash(ctx context.Context, rawURL string) (string, error) {
	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"getGenesisHash","params":[]}`)
	var decoded strResult
	if err := p.post(ctx, rawURL, payload, &decoded); err != nil {
		return "", err
	}
	if decoded.Error != nil {
		return "", fmt.Errorf("rpc error %d", decoded.Error.Code)
	}
	genesis := strings.TrimSpace(decoded.Result)
	if genesis == "" {
		return "", fmt.Errorf("empty genesis hash")
	}
	return genesis, nil
}

func (p *Prober) callChainID(ctx context.Context, rawURL string) (int64, error) {
	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}`)
	var decoded hexResult
	if err := p.post(ctx, rawURL, payload, &decoded); err != nil {
		return 0, err
	}
	if decoded.Error != nil {
		return 0, fmt.Errorf("rpc error %d", decoded.Error.Code)
	}
	value := new(big.Int)
	trimmed := strings.TrimPrefix(decoded.Result, "0x")
	if _, ok := value.SetString(trimmed, 16); !ok {
		return 0, fmt.Errorf("invalid chain id")
	}
	return value.Int64(), nil
}

func (p *Prober) callBlockNumber(ctx context.Context, rawURL string) (uint64, error) {
	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}`)
	var decoded hexResult
	if err := p.post(ctx, rawURL, payload, &decoded); err != nil {
		return 0, err
	}
	if decoded.Error != nil {
		return 0, fmt.Errorf("rpc error %d", decoded.Error.Code)
	}
	value := new(big.Int)
	trimmed := strings.TrimPrefix(decoded.Result, "0x")
	if _, ok := value.SetString(trimmed, 16); !ok {
		return 0, fmt.Errorf("invalid block number")
	}
	return value.Uint64(), nil
}

func (p *Prober) callSolanaHealth(ctx context.Context, rawURL string) (bool, error) {
	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"getHealth","params":[]}`)
	var decoded strResult
	if err := p.post(ctx, rawURL, payload, &decoded); err != nil {
		return false, err
	}
	if decoded.Error != nil {
		return false, nil
	}
	return decoded.Result == "ok", nil
}

func (p *Prober) callSolanaSlot(ctx context.Context, rawURL string) (uint64, error) {
	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"getSlot","params":[]}`)
	var decoded u64Result
	if err := p.post(ctx, rawURL, payload, &decoded); err != nil {
		return 0, err
	}
	if decoded.Error != nil {
		return 0, fmt.Errorf("rpc error %d", decoded.Error.Code)
	}
	return decoded.Result, nil
}

func (p *Prober) post(ctx context.Context, rawURL string, payload []byte, decoded any) error {
	withTimeout, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(withTimeout, http.MethodPost, rawURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build probe for %s: %w", maskEndpoint(rawURL), err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return fmt.Errorf("probe %s failed", maskEndpoint(rawURL))
	}
	defer response.Body.Close()
	if response.StatusCode == rateLimitStatus {
		return fmt.Errorf("probe %s: rate limited", maskEndpoint(rawURL))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("probe %s: http %d", maskEndpoint(rawURL), response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("read probe %s: %w", maskEndpoint(rawURL), err)
	}
	if err := json.Unmarshal(body, decoded); err != nil {
		return fmt.Errorf("decode probe %s: %w", maskEndpoint(rawURL), err)
	}
	return nil
}

func isRateLimited(err error) bool {
	return err != nil && strings.Contains(err.Error(), "rate limited")
}

func chainIDText(chainID int64) string {
	return fmt.Sprintf("%d", chainID)
}
