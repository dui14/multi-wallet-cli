package evm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"multi-wallet-cli/infra/logging"
)

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const maxResponseBytes = 8 * 1024 * 1024

type rawResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func (c *Client) callRaw(ctx context.Context, rpc, method string, params []any) (json.RawMessage, error) {
	payload, err := json.Marshal(request{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", method, err)
	}
	res, err := c.post(ctx, rpc, payload)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	var decoded rawResponse
	if err := json.Unmarshal(res, &decoded); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", method, err)
	}
	if decoded.Error != nil {
		noteJSONRPC(ctx, decoded.Error)
		return nil, fmt.Errorf("%s: %s", method, decoded.Error.Message)
	}
	return decoded.Result, nil
}

func (c *Client) post(ctx context.Context, rpc string, payload []byte) ([]byte, error) {
	req, err := newRequest(ctx, rpc, payload)
	if err != nil {
		return nil, err
	}
	res, err := c.httpClient.Do(req)
	if err != nil {
		logging.NoteRPCFailure(ctx, "timeout/transport: "+err.Error())
		return nil, fmt.Errorf("call EVM RPC %s", logging.MaskRPCURL(rpc))
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		logging.NoteRPCFailure(ctx, "HTTP "+res.Status)
		return nil, fmt.Errorf("EVM RPC %s returned HTTP %s", logging.MaskRPCURL(rpc), res.Status)
	}
	return readLimited(res.Body)
}

// PendingNonce reads the transaction count at "pending" for address on rpc.
func (c *Client) PendingNonce(ctx context.Context, rpc, address string) (uint64, error) {
	result, err := c.callRaw(ctx, rpc, "eth_getTransactionCount", []any{address, "pending"})
	if err != nil {
		return 0, err
	}
	var encoded string
	if err := json.Unmarshal(result, &encoded); err != nil {
		return 0, fmt.Errorf("decode nonce: %w", err)
	}
	value, err := parseHex(encoded)
	if err != nil {
		return 0, fmt.Errorf("parse nonce: %w", err)
	}
	if !value.IsUint64() {
		return 0, fmt.Errorf("nonce out of range")
	}
	return value.Uint64(), nil
}

// CrossPendingNonce reads the pending nonce from two independent nodes and
// only agrees when they match; a third node breaks a mismatch by majority.
func (c *Client) CrossPendingNonce(ctx context.Context, nodes []string, address string) (uint64, error) {
	return crossRead(ctx, nodes, func(ctx context.Context, rpc string) (uint64, error) {
		return c.PendingNonce(ctx, rpc, address)
	}, func(first, second uint64) bool { return first == second })
}

func newRequest(ctx context.Context, rpc string, payload []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpc, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create rpc request for %s", logging.MaskRPCURL(rpc))
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func noteJSONRPC(ctx context.Context, rpcErr *rpcError) {
	if rpcErr == nil {
		return
	}
	logging.NoteRPCFailure(ctx, fmt.Sprintf("json-rpc code=%d message=%s", rpcErr.Code, rpcErr.Message))
}

func readLimited(body io.ReadCloser) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read rpc response: %w", err)
	}
	return data, nil
}

func crossRead[T any](ctx context.Context, nodes []string, fetch func(context.Context, string) (T, error), equal func(first, second T) bool) (T, error) {
	var zero T
	if len(nodes) == 0 {
		return zero, fmt.Errorf("cross read: no nodes")
	}
	first, err := fetch(ctx, nodes[0])
	if err != nil {
		return zero, fmt.Errorf("cross read node 1: %w", err)
	}
	if len(nodes) == 1 {
		return first, nil
	}
	second, err := fetch(ctx, nodes[1])
	if err != nil {
		return zero, fmt.Errorf("cross read node 2: %w", err)
	}
	if equal(first, second) {
		return first, nil
	}
	if len(nodes) < 3 {
		return zero, fmt.Errorf("cross read mismatch between nodes and no third node")
	}
	third, err := fetch(ctx, nodes[2])
	if err != nil {
		return zero, fmt.Errorf("cross read node 3: %w", err)
	}
	if equal(third, first) {
		return first, nil
	}
	if equal(third, second) {
		return second, nil
	}
	return zero, fmt.Errorf("cross read mismatch across three nodes")
}
