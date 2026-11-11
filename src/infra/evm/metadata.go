package evm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// HasCode reports whether address contains deployed bytecode via eth_getCode.
func (c *Client) HasCode(ctx context.Context, rpc, address string) (bool, error) {
	result, err := c.callRaw(ctx, rpc, "eth_getCode", []any{address, "latest"})
	if err != nil {
		return false, err
	}
	var encoded string
	if err := json.Unmarshal(result, &encoded); err != nil {
		return false, fmt.Errorf("decode code: %w", err)
	}
	trimmed := encoded
	if len(trimmed) >= 2 && (trimmed[:2] == "0x" || trimmed[:2] == "0X") {
		trimmed = trimmed[2:]
	}
	for _, ch := range trimmed {
		if ch != '0' {
			return true, nil
		}
	}
	return false, nil
}

// ReadSymbol reads ERC20 symbol() via eth_call and returns it as text.
func (c *Client) ReadSymbol(ctx context.Context, rpc, address string) (string, error) {
	result, err := c.callRaw(ctx, rpc, "eth_call", []any{map[string]string{"to": address, "data": "0x95d89b41"}, "latest"})
	if err != nil {
		return "", err
	}
	return decodeABIString(result)
}

// ReadName reads ERC20 name() via eth_call and returns it as text.
func (c *Client) ReadName(ctx context.Context, rpc, address string) (string, error) {
	result, err := c.callRaw(ctx, rpc, "eth_call", []any{map[string]string{"to": address, "data": "0x06fdde03"}, "latest"})
	if err != nil {
		return "", err
	}
	return decodeABIString(result)
}

// ReadDecimals reads ERC20 decimals() via eth_call.
func (c *Client) ReadDecimals(ctx context.Context, rpc, address string) (uint8, error) {
	result, err := c.callRaw(ctx, rpc, "eth_call", []any{map[string]string{"to": address, "data": "0x313ce567"}, "latest"})
	if err != nil {
		return 0, err
	}
	value, err := decodeBigQuantity(result, "decimals")
	if err != nil {
		return 0, err
	}
	if !value.IsUint64() || value.Uint64() > 255 {
		return 0, fmt.Errorf("decimals out of range")
	}
	return uint8(value.Uint64()), nil
}

// Simulate runs eth_call for a transfer-shaped tx to validate it would succeed.
func (c *Client) Simulate(ctx context.Context, rpc string, tx TransferTx) error {
	call, err := callParams(tx)
	if err != nil {
		return err
	}
	_, err = c.callRaw(ctx, rpc, "eth_call", []any{call, "latest"})
	return err
}

// BlockGasLimit reads the current block gas limit via eth_getBlockByNumber.
func (c *Client) BlockGasLimit(ctx context.Context, rpc string) (uint64, error) {
	result, err := c.callRaw(ctx, rpc, "eth_getBlockByNumber", []any{"latest", false})
	if err != nil {
		return 0, err
	}
	var block struct {
		GasLimit string `json:"gasLimit"`
	}
	if err := json.Unmarshal(result, &block); err != nil {
		return 0, fmt.Errorf("decode block: %w", err)
	}
	value, err := decodeBigQuantity([]byte(quote(block.GasLimit)), "gas limit")
	if err != nil {
		return 0, err
	}
	if !value.IsUint64() {
		return 0, fmt.Errorf("gas limit out of range")
	}
	return value.Uint64(), nil
}

// erc20BalanceData encodes balanceOf(owner) calldata.
func erc20BalanceData(owner string) ([]byte, error) {
	if !common.IsHexAddress(owner) {
		return nil, fmt.Errorf("invalid owner address")
	}
	selector := []byte{0x70, 0xa0, 0x82, 0x31}
	address := common.HexToAddress(owner)
	data := make([]byte, 0, 36)
	data = append(data, selector...)
	padded := make([]byte, 32)
	copy(padded[12:], address.Bytes())
	return append(data, padded...), nil
}

// NativeBalance reads the ETH/BNB balance of address.
func (c *Client) NativeBalance(ctx context.Context, rpc, address string) (*big.Int, error) {
	result, err := c.callRaw(ctx, rpc, "eth_getBalance", []any{address, "latest"})
	if err != nil {
		return nil, err
	}
	return decodeBigQuantity(result, "balance")
}

// TokenBalance reads the ERC20 balanceOf(owner) of tokenAddress.
func (c *Client) TokenBalance(ctx context.Context, rpc, owner, tokenAddress string) (*big.Int, error) {
	packed, err := erc20BalanceData(owner)
	if err != nil {
		return nil, err
	}
	result, err := c.callRaw(ctx, rpc, "eth_call", []any{map[string]string{"to": tokenAddress, "data": "0x" + fmt.Sprintf("%x", packed)}, "latest"})
	if err != nil {
		return nil, err
	}
	return decodeBigQuantity(result, "token balance")
}

// Receipt carries the mined fields we need to track a broadcast.
type Receipt struct {
	Status            uint64
	BlockNumber       *big.Int
	GasUsed           *big.Int
	EffectiveGasPrice *big.Int
	TxHash            string
}

const receiptPollInterval = time.Second

// GetReceipt reads eth_getTransactionReceipt once. A pending or missing
// receipt returns (nil, nil) so callers can poll another endpoint.
func (c *Client) GetReceipt(ctx context.Context, rpc, txHash string) (*Receipt, error) {
	txHash = normalizeTxHash(txHash)
	result, err := c.callRaw(ctx, rpc, "eth_getTransactionReceipt", []any{txHash})
	if err != nil {
		return nil, err
	}
	return parseReceipt(result, txHash)
}

// TxLookup is the mempool or mined view of a transaction hash.
type TxLookup struct {
	Hash        string
	BlockNumber *big.Int
}

// LookupTx reads eth_getTransactionByHash. Pending or missing returns (nil, nil).
func (c *Client) LookupTx(ctx context.Context, rpc, txHash string) (*TxLookup, error) {
	txHash = normalizeTxHash(txHash)
	result, err := c.callRaw(ctx, rpc, "eth_getTransactionByHash", []any{txHash})
	if err != nil {
		return nil, err
	}
	return parseTxLookup(result, txHash)
}

// HasTransaction reports whether the node knows this hash in mempool or chain.
func (c *Client) HasTransaction(ctx context.Context, rpc, txHash string) (bool, error) {
	lookup, err := c.LookupTx(ctx, rpc, txHash)
	if err != nil {
		return false, err
	}
	return lookup != nil, nil
}

// ConfirmTx returns a mined receipt from this node. If the tx is included but
// the receipt is still lagging, it returns the block number with a successful
// status so callers can finish instead of spinning.
func (c *Client) ConfirmTx(ctx context.Context, rpc, txHash string) (*Receipt, error) {
	txHash = normalizeTxHash(txHash)
	receipt, err := c.GetReceipt(ctx, rpc, txHash)
	if err != nil {
		return nil, err
	}
	if receipt != nil {
		return receipt, nil
	}
	lookup, err := c.LookupTx(ctx, rpc, txHash)
	if err != nil {
		return nil, err
	}
	if lookup == nil || lookup.BlockNumber == nil {
		return nil, nil
	}
	receipt, err = c.GetReceipt(ctx, rpc, txHash)
	if err != nil {
		return nil, err
	}
	if receipt != nil {
		return receipt, nil
	}
	return &Receipt{Status: 1, BlockNumber: lookup.BlockNumber, TxHash: txHash}, nil
}

// WaitReceipt polls eth_getTransactionReceipt until the tx is mined or the
// timeout elapses. It returns (nil, nil) on timeout so callers can retry.
func (c *Client) WaitReceipt(ctx context.Context, rpc, txHash string, timeout time.Duration) (*Receipt, error) {
	deadline := time.Now().Add(timeout)
	for {
		receipt, err := c.GetReceipt(ctx, rpc, txHash)
		if err == nil && receipt != nil {
			return receipt, nil
		}
		if time.Now().After(deadline) {
			return nil, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(receiptPollInterval):
		}
	}
}

func parseTxLookup(result json.RawMessage, txHash string) (*TxLookup, error) {
	trimmed := bytes.TrimSpace(result)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	var decoded struct {
		Hash        string          `json:"hash"`
		BlockNumber json.RawMessage `json:"blockNumber"`
	}
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		return nil, fmt.Errorf("decode transaction: %w", err)
	}
	lookup := &TxLookup{Hash: txHash}
	if decoded.Hash != "" {
		lookup.Hash = decoded.Hash
	}
	block := bytes.TrimSpace(decoded.BlockNumber)
	if len(block) > 0 && string(block) != "null" && string(block) != `""` {
		value, err := decodeBigQuantity(block, "block number")
		if err != nil {
			return nil, err
		}
		lookup.BlockNumber = value
	}
	return lookup, nil
}

func normalizeTxHash(txHash string) string {
	trimmed := strings.TrimSpace(txHash)
	if trimmed == "" {
		return trimmed
	}
	if len(trimmed) >= 2 && (trimmed[:2] == "0x" || trimmed[:2] == "0X") {
		trimmed = trimmed[2:]
	}
	return "0x" + strings.ToLower(trimmed)
}

func parseReceipt(result json.RawMessage, txHash string) (*Receipt, error) {
	trimmed := bytes.TrimSpace(result)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	var decoded struct {
		Status            json.RawMessage `json:"status"`
		BlockNumber       json.RawMessage `json:"blockNumber"`
		GasUsed           json.RawMessage `json:"gasUsed"`
		EffectiveGasPrice json.RawMessage `json:"effectiveGasPrice"`
	}
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		return nil, fmt.Errorf("decode receipt: %w", err)
	}
	if len(bytes.TrimSpace(decoded.BlockNumber)) == 0 || string(bytes.TrimSpace(decoded.BlockNumber)) == "null" {
		return nil, nil
	}
	blockNumber, err := decodeBigQuantity(decoded.BlockNumber, "block number")
	if err != nil {
		return nil, err
	}
	status := uint64(1)
	if len(bytes.TrimSpace(decoded.Status)) > 0 && string(bytes.TrimSpace(decoded.Status)) != "null" {
		value, statusErr := decodeBigQuantity(decoded.Status, "receipt status")
		if statusErr != nil {
			return nil, statusErr
		}
		status = value.Uint64()
	}
	receipt := &Receipt{Status: status, BlockNumber: blockNumber, TxHash: txHash}
	if gasUsed, gasErr := decodeBigQuantity(decoded.GasUsed, "gas used"); gasErr == nil {
		receipt.GasUsed = gasUsed
	}
	if price, priceErr := decodeBigQuantity(decoded.EffectiveGasPrice, "effective gas price"); priceErr == nil {
		receipt.EffectiveGasPrice = price
	}
	return receipt, nil
}

func quote(value string) string {
	if value == "" {
		return `""`
	}
	return `"` + value + `"`
}

func decodeABIString(raw json.RawMessage) (string, error) {
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return "", fmt.Errorf("decode abi string: %w", err)
	}
	trimmed := encoded
	if len(trimmed) >= 2 && (trimmed[:2] == "0x" || trimmed[:2] == "0X") {
		trimmed = trimmed[2:]
	}
	if strings.Trim(trimmed, "0") == "" {
		return "", fmt.Errorf("token contract returned empty metadata (not ERC20?)")
	}
	if len(trimmed)%2 == 1 {
		return "", fmt.Errorf("token contract returned malformed metadata")
	}
	data, err := decodeHexString(trimmed)
	if err != nil {
		return "", err
	}
	if len(data) == 32 {
		return string(bytes.TrimRight(data, "\x00")), nil
	}
	if len(data) < 64 {
		return "", fmt.Errorf("token contract returned invalid metadata (not ERC20?)")
	}
	offset := new(big.Int).SetBytes(data[0:32])
	if !offset.IsUint64() || offset.Uint64() != 32 {
		return "", fmt.Errorf("token contract returned invalid metadata (not ERC20?)")
	}
	length := new(big.Int).SetBytes(data[32:64])
	if !length.IsUint64() {
		return "", fmt.Errorf("token contract returned invalid metadata (not ERC20?)")
	}
	length64 := length.Uint64()
	if length64 > 1024 {
		return "", fmt.Errorf("token contract returned invalid metadata (not ERC20?)")
	}
	start := 64
	if uint64(len(data)) < uint64(start)+length64 {
		return "", fmt.Errorf("token contract returned truncated metadata")
	}
	value := string(data[start : start+int(length64)])
	return string(bytes.TrimRight([]byte(value), "\x00")), nil
}

func decodeHexString(value string) ([]byte, error) {
	out := make([]byte, len(value)/2)
	for index := 0; index < len(out); index++ {
		high, err := hexNibble(value[index*2])
		if err != nil {
			return nil, err
		}
		low, err := hexNibble(value[index*2+1])
		if err != nil {
			return nil, err
		}
		out[index] = high<<4 | low
	}
	return out, nil
}

func hexNibble(ch byte) (byte, error) {
	switch {
	case ch >= '0' && ch <= '9':
		return ch - '0', nil
	case ch >= 'a' && ch <= 'f':
		return ch - 'a' + 10, nil
	case ch >= 'A' && ch <= 'F':
		return ch - 'A' + 10, nil
	}
	return 0, fmt.Errorf("invalid hex digit %q", string(ch))
}
