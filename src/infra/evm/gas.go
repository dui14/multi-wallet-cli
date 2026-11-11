package evm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// FeeEstimate carries legacy and EIP-1559 fee fields for one chain.
// MaxFeePerGas and MaxPriorityFeePerGas are nil when the node only supports
// legacy pricing; callers then fall back to GasPrice.
type FeeEstimate struct {
	GasPrice             *big.Int
	MaxFeePerGas         *big.Int
	MaxPriorityFeePerGas *big.Int
}

type feeHistoryResult struct {
	BaseFeePerGas []json.RawMessage   `json:"baseFeePerGas"`
	Reward        [][]json.RawMessage `json:"reward"`
}

// GasPrice reads eth_gasPrice from rpc.
func (c *Client) GasPrice(ctx context.Context, rpc string) (*big.Int, error) {
	result, err := c.callRaw(ctx, rpc, "eth_gasPrice", nil)
	if err != nil {
		return nil, err
	}
	return decodeBigQuantity(result, "gas price")
}

// CrossGasPrice reads gas price from two independent nodes and requires an
// equal value; a third node breaks a mismatch by majority.
func (c *Client) CrossGasPrice(ctx context.Context, nodes []string) (*big.Int, error) {
	return crossRead(ctx, nodes, c.GasPrice, func(first, second *big.Int) bool {
		return first.Cmp(second) == 0
	})
}

// EstimateFees prefers eth_feeHistory (base fee plus 25th percentile tip)
// and falls back to eth_gasPrice when fee history is unavailable. EIP-1559
// fields are always filled so 1559 chains never sign a legacy transaction.
func (c *Client) EstimateFees(ctx context.Context, rpc string) (*FeeEstimate, error) {
	result, err := c.callRaw(ctx, rpc, "eth_feeHistory", []any{4, "latest", []any{50, 75}})
	if err == nil {
		estimate, buildErr := buildFeeEstimate(result)
		if buildErr == nil {
			return c.enrichFees(ctx, rpc, estimate), nil
		}
	}
	gasPrice, gasErr := c.GasPrice(ctx, rpc)
	if gasErr != nil {
		if err != nil {
			return nil, fmt.Errorf("fee history: %v; gas price: %w", err, gasErr)
		}
		return nil, gasErr
	}
	return c.enrichFees(ctx, rpc, &FeeEstimate{GasPrice: gasPrice}), nil
}

func (c *Client) enrichFees(ctx context.Context, rpc string, estimate *FeeEstimate) *FeeEstimate {
	estimate = EnsureEIP1559(estimate)
	if estimate.MaxPriorityFeePerGas.Sign() > 0 {
		return estimate
	}
	tip, err := c.MaxPriorityFee(ctx, rpc)
	if err != nil || tip == nil || tip.Sign() <= 0 {
		return estimate
	}
	estimate.MaxPriorityFeePerGas = tip
	price := estimate.GasPrice
	if price == nil {
		price = big.NewInt(0)
	}
	candidate := new(big.Int).Add(new(big.Int).Mul(price, big.NewInt(3)), tip)
	if estimate.MaxFeePerGas == nil || estimate.MaxFeePerGas.Cmp(candidate) < 0 {
		estimate.MaxFeePerGas = candidate
	}
	if estimate.MaxFeePerGas.Cmp(tip) < 0 {
		estimate.MaxFeePerGas = new(big.Int).Set(tip)
	}
	return estimate
}

// MaxPriorityFee reads eth_maxPriorityFeePerGas.
func (c *Client) MaxPriorityFee(ctx context.Context, rpc string) (*big.Int, error) {
	result, err := c.callRaw(ctx, rpc, "eth_maxPriorityFeePerGas", nil)
	if err != nil {
		return nil, err
	}
	return decodeBigQuantity(result, "priority fee")
}

// EnsureEIP1559 fills MaxFeePerGas and MaxPriorityFeePerGas from GasPrice when
// a node only returned legacy pricing.
func EnsureEIP1559(fees *FeeEstimate) *FeeEstimate {
	if fees == nil {
		return nil
	}
	out := *fees
	if out.MaxPriorityFeePerGas == nil {
		if out.GasPrice != nil {
			out.MaxPriorityFeePerGas = new(big.Int).Set(out.GasPrice)
		} else {
			out.MaxPriorityFeePerGas = big.NewInt(0)
		}
	}
	if out.MaxFeePerGas == nil {
		price := out.GasPrice
		if price == nil {
			price = out.MaxPriorityFeePerGas
		}
		if price == nil {
			price = big.NewInt(0)
		}
		out.MaxFeePerGas = new(big.Int).Mul(price, big.NewInt(3))
	}
	if out.MaxFeePerGas.Cmp(out.MaxPriorityFeePerGas) < 0 {
		out.MaxFeePerGas = new(big.Int).Set(out.MaxPriorityFeePerGas)
	}
	if out.GasPrice == nil {
		out.GasPrice = new(big.Int).Set(out.MaxFeePerGas)
	}
	return &out
}

// MergeFeeEstimates keeps the highest fee fields so a lagging RPC cannot
// underprice a broadcast.
func MergeFeeEstimates(items []*FeeEstimate) *FeeEstimate {
	var merged *FeeEstimate
	for _, item := range items {
		if item == nil {
			continue
		}
		ready := EnsureEIP1559(item)
		if merged == nil {
			copyItem := *ready
			merged = &copyItem
			continue
		}
		merged.GasPrice = maxBig(merged.GasPrice, ready.GasPrice)
		merged.MaxFeePerGas = maxBig(merged.MaxFeePerGas, ready.MaxFeePerGas)
		merged.MaxPriorityFeePerGas = maxBig(merged.MaxPriorityFeePerGas, ready.MaxPriorityFeePerGas)
	}
	return EnsureEIP1559(merged)
}

func maxBig(left, right *big.Int) *big.Int {
	if left == nil {
		if right == nil {
			return nil
		}
		return new(big.Int).Set(right)
	}
	if right == nil || left.Cmp(right) >= 0 {
		return new(big.Int).Set(left)
	}
	return new(big.Int).Set(right)
}

func buildFeeEstimate(result json.RawMessage) (*FeeEstimate, error) {
	var history feeHistoryResult
	if err := json.Unmarshal(result, &history); err != nil {
		return nil, fmt.Errorf("decode fee history: %w", err)
	}
	if len(history.BaseFeePerGas) < 2 {
		return nil, fmt.Errorf("fee history missing next base fee")
	}
	nextBase, err := decodeBigQuantity(history.BaseFeePerGas[len(history.BaseFeePerGas)-1], "base fee")
	if err != nil {
		return nil, fmt.Errorf("parse base fee: %w", err)
	}
	tip := big.NewInt(0)
	if len(history.Reward) > 0 {
		last := history.Reward[len(history.Reward)-1]
		if len(last) > 0 {
			parsed, parseErr := decodeBigQuantity(last[len(last)-1], "priority fee")
			if parseErr != nil {
				return nil, fmt.Errorf("parse priority fee: %w", parseErr)
			}
			tip = parsed
		}
	}
	maxFee := new(big.Int).Add(new(big.Int).Mul(nextBase, big.NewInt(3)), tip)
	return &FeeEstimate{
		GasPrice:             new(big.Int).Add(nextBase, tip),
		MaxFeePerGas:         maxFee,
		MaxPriorityFeePerGas: tip,
	}, nil
}

func decodeBigQuantity(result json.RawMessage, label string) (*big.Int, error) {
	trimmed := bytes.TrimSpace(result)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, fmt.Errorf("decode %s: empty", label)
	}
	if trimmed[0] == '"' {
		var encoded string
		if err := json.Unmarshal(trimmed, &encoded); err != nil {
			return nil, fmt.Errorf("decode %s: %w", label, err)
		}
		return parseQuantityString(encoded, label)
	}
	var number json.Number
	if err := json.Unmarshal(trimmed, &number); err != nil {
		return nil, fmt.Errorf("decode %s: %w", label, err)
	}
	value := new(big.Int)
	if _, ok := value.SetString(number.String(), 10); !ok {
		return nil, fmt.Errorf("parse %s", label)
	}
	return value, nil
}

func parseQuantityString(encoded, label string) (*big.Int, error) {
	encoded = strings.TrimSpace(encoded)
	if len(encoded) >= 2 && (encoded[:2] == "0x" || encoded[:2] == "0X") {
		value, err := parseHex(encoded)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", label, err)
		}
		return value, nil
	}
	value := new(big.Int)
	if _, ok := value.SetString(encoded, 10); ok {
		return value, nil
	}
	value, err := parseHex(encoded)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", label, err)
	}
	return value, nil
}
