package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Blast behaviour constants.
const (
	blastTimeout = 20 * time.Second
)

// ErrBlastNotConfigured is returned when an urgent transaction needs Blast
// but no endpoint is configured for the chain.
var ErrBlastNotConfigured = errors.New("blast endpoint not configured for urgent transaction")

// Blast broadcasts urgent transactions in parallel through the current
// endpoint and a dedicated backup endpoint, taking the first success.
type Blast struct {
	client    *http.Client
	evmURL    string
	solanaURL string
}

// NewBlast builds a Blast helper; empty URLs mean that VM is unconfigured.
func NewBlast(client *http.Client, evmURL string, solanaURL string) *Blast {
	if client == nil {
		client = http.DefaultClient
	}
	return &Blast{client: client, evmURL: strings.TrimSpace(evmURL), solanaURL: strings.TrimSpace(solanaURL)}
}

// BroadcastEVM sends rawTx to primary and the EVM blast endpoint at once
// and returns the first accepted transaction hash.
func (b *Blast) BroadcastEVM(ctx context.Context, primaryURL string, rawTx string) (string, error) {
	if b.evmURL == "" {
		return "", ErrBlastNotConfigured
	}
	type outcome struct {
		hash string
		err  error
	}
	send := func(target string) (string, error) {
		payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_sendRawTransaction","params":["` + rawTx + `"]}`)
		var decoded struct {
			Result string    `json:"result"`
			Error  *rpcError `json:"error"`
		}
		if err := blastPost(ctx, b.client, target, payload, &decoded); err != nil {
			return "", err
		}
		if decoded.Error != nil {
			return "", fmt.Errorf("rpc error %d", decoded.Error.Code)
		}
		if decoded.Result == "" {
			return "", fmt.Errorf("empty transaction hash")
		}
		return decoded.Result, nil
	}
	results := make(chan outcome, 2)
	blastCtx, cancel := context.WithTimeout(ctx, blastTimeout)
	defer cancel()
	for _, target := range []string{primaryURL, b.evmURL} {
		target := target
		go func() {
			hash, err := send(target)
			results <- outcome{hash: hash, err: err}
		}()
	}
	var failures []string
	for i := 0; i < 2; i++ {
		select {
		case result := <-results:
			if result.err == nil {
				return result.hash, nil
			}
			failures = append(failures, result.err.Error())
		case <-blastCtx.Done():
			return "", fmt.Errorf("blast broadcast timed out")
		}
	}
	return "", fmt.Errorf("blast broadcast failed on both paths: %s", strings.Join(failures, "; "))
}

// BroadcastSolana sends the base64 signed transaction to primary and the
// Solana blast endpoint at once, returning the first accepted signature.
func (b *Blast) BroadcastSolana(ctx context.Context, primaryURL string, txBase64 string) (string, error) {
	if b.solanaURL == "" {
		return "", ErrBlastNotConfigured
	}
	type outcome struct {
		signature string
		err       error
	}
	send := func(target string) (string, error) {
		params := []any{txBase64, map[string]any{"encoding": "base64"}}
		payload, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "sendTransaction",
			"params":  params,
		})
		if err != nil {
			return "", err
		}
		var decoded struct {
			Result string    `json:"result"`
			Error  *rpcError `json:"error"`
		}
		if err := blastPost(ctx, b.client, target, payload, &decoded); err != nil {
			return "", err
		}
		if decoded.Error != nil {
			return "", fmt.Errorf("rpc error %d", decoded.Error.Code)
		}
		if decoded.Result == "" {
			return "", fmt.Errorf("empty signature")
		}
		return decoded.Result, nil
	}
	results := make(chan outcome, 2)
	blastCtx, cancel := context.WithTimeout(ctx, blastTimeout)
	defer cancel()
	for _, target := range []string{primaryURL, b.solanaURL} {
		target := target
		go func() {
			signature, err := send(target)
			results <- outcome{signature: signature, err: err}
		}()
	}
	var failures []string
	for i := 0; i < 2; i++ {
		select {
		case result := <-results:
			if result.err == nil {
				return result.signature, nil
			}
			failures = append(failures, result.err.Error())
		case <-blastCtx.Done():
			return "", fmt.Errorf("blast broadcast timed out")
		}
	}
	return "", fmt.Errorf("blast broadcast failed on both paths: %s", strings.Join(failures, "; "))
}

func blastPost(ctx context.Context, client *http.Client, rawURL string, payload []byte, decoded any) error {
	withTimeout, cancel := context.WithTimeout(ctx, blastTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(withTimeout, http.MethodPost, rawURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build blast request for %s", maskEndpoint(rawURL))
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("blast %s failed", maskEndpoint(rawURL))
	}
	defer response.Body.Close()
	if response.StatusCode == rateLimitStatus {
		return fmt.Errorf("blast %s: rate limited", maskEndpoint(rawURL))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("blast %s: http %d", maskEndpoint(rawURL), response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("read blast %s", maskEndpoint(rawURL))
	}
	if err := json.Unmarshal(body, decoded); err != nil {
		return fmt.Errorf("decode blast %s", maskEndpoint(rawURL))
	}
	return nil
}
