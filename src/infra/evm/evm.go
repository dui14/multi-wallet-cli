package evm

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

type Token struct {
	Symbol   string
	Address  string
	Decimals uint8
}
type Result struct {
	Symbol   string
	Native   bool
	Raw      *big.Int
	Decimals uint8
	Err      error
}
type Client struct{ httpClient *http.Client }

func NewClient() *Client { return &Client{httpClient: &http.Client{Timeout: 20 * time.Second}} }

func (c *Client) Scan(ctx context.Context, rpc, address, nativeSymbol string, nativeDecimals uint8, tokens []Token, includeNative bool) ([]Result, error) {
	requests := make([]request, 0, len(tokens)*2+1)
	if includeNative {
		requests = append(requests, request{"2.0", 1, "eth_getBalance", []any{address, "latest"}})
	}
	for index, token := range tokens {
		data := "0x70a08231" + strings.Repeat("0", 24) + strings.TrimPrefix(strings.ToLower(address), "0x")
		idOffset := 1
		if includeNative {
			idOffset = 2
		}
		requests = append(requests,
			request{"2.0", index*2 + idOffset, "eth_call", []any{map[string]string{"to": token.Address, "data": data}, "latest"}},
			request{"2.0", index*2 + idOffset + 1, "eth_call", []any{map[string]string{"to": token.Address, "data": "0x313ce567"}, "latest"}},
		)
	}
	responses, err := c.batch(ctx, rpc, requests)
	if err != nil {
		return nil, err
	}
	byID := make(map[int]response, len(responses))
	for _, item := range responses {
		byID[item.ID] = item
	}
	results := make([]Result, 0, len(tokens)+1)
	if includeNative {
		native, nativeErr := parseHex(byID[1].Result)
		if byID[1].Error != nil {
			nativeErr = fmt.Errorf("native balance: %s", byID[1].Error.Message)
		}
		results = append(results, Result{Symbol: nativeSymbol, Native: true, Raw: native, Decimals: nativeDecimals, Err: nativeErr})
	}
	for index, token := range tokens {
		idOffset := 1
		if includeNative {
			idOffset = 2
		}
		balanceResponse, decimalsResponse := byID[index*2+idOffset], byID[index*2+idOffset+1]
		result := Result{Symbol: token.Symbol, Decimals: token.Decimals}
		if balanceResponse.Error != nil {
			result.Err = fmt.Errorf("balance: %s", balanceResponse.Error.Message)
		} else {
			result.Raw, result.Err = parseHex(balanceResponse.Result)
			if decimalsResponse.Error == nil {
				if value, parseErr := parseHex(decimalsResponse.Result); parseErr == nil && value.IsUint64() {
					result.Decimals = uint8(value.Uint64())
				}
			}
		}
		results = append(results, result)
	}
	return results, nil
}

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}
type response struct {
	ID     int    `json:"id"`
	Result string `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) batch(ctx context.Context, rpc string, requests []request) ([]response, error) {
	payload, err := json.Marshal(requests)
	if err != nil {
		return nil, fmt.Errorf("encode rpc batch: %w", err)
	}
	res, err := c.post(ctx, rpc, payload)
	if err != nil {
		return nil, err
	}
	var responses []response
	if err := json.Unmarshal(res, &responses); err != nil {
		return nil, fmt.Errorf("decode rpc response: %w", err)
	}
	return responses, nil
}

func parseHex(value string) (*big.Int, error) {
	value = strings.TrimPrefix(value, "0x")
	if value == "" {
		return big.NewInt(0), nil
	}
	result := new(big.Int)
	if _, ok := result.SetString(value, 16); !ok {
		return nil, fmt.Errorf("invalid numeric result")
	}
	return result, nil
}

const erc20ABIJSON = `[{"constant":false,"inputs":[{"name":"to","type":"address"},{"name":"value","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"type":"function"},{"constant":false,"inputs":[{"name":"spender","type":"address"},{"name":"value","type":"uint256"}],"name":"approve","outputs":[{"name":"","type":"bool"}],"type":"function"},{"constant":true,"inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"}],"name":"allowance","outputs":[{"name":"","type":"uint256"}],"type":"function"}]`

var parsedERC20ABI abi.ABI

func erc20ABI() (abi.ABI, error) {
	if len(parsedERC20ABI.Methods) != 0 {
		return parsedERC20ABI, nil
	}
	parsed, err := abi.JSON(strings.NewReader(erc20ABIJSON))
	if err != nil {
		return abi.ABI{}, err
	}
	parsedERC20ABI = parsed
	return parsed, nil
}

// PackERC20Transfer encodes transfer(to, value) calldata.
func PackERC20Transfer(to string, amount *big.Int) ([]byte, error) {
	if !common.IsHexAddress(to) {
		return nil, fmt.Errorf("invalid recipient address")
	}
	if amount == nil || amount.Sign() <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	packedABI, err := erc20ABI()
	if err != nil {
		return nil, err
	}
	return packedABI.Pack("transfer", common.HexToAddress(to), amount)
}

// PackERC20Approve encodes approve(spender, value) calldata.
func PackERC20Approve(spender string, amount *big.Int) ([]byte, error) {
	if !common.IsHexAddress(spender) {
		return nil, fmt.Errorf("invalid spender address")
	}
	if amount == nil || amount.Sign() < 0 {
		return nil, fmt.Errorf("amount must not be negative")
	}
	packedABI, err := erc20ABI()
	if err != nil {
		return nil, err
	}
	return packedABI.Pack("approve", common.HexToAddress(spender), amount)
}

// TransferTx bundles unsigned EVM transfer fields for one broadcast.
// Auth is set only for a type-4 transaction and is included in gas estimation.
type TransferTx struct {
	From     string
	To       string
	Value    *big.Int
	Data     []byte
	GasLimit uint64
	Auth     *types.SetCodeAuthorization
}

// BuildTransferTx creates an unsigned tx skeleton for native or ERC20 transfer.
func BuildTransferTx(from string, tokenAddress string, recipient string, amount *big.Int, gasLimit uint64) (TransferTx, error) {
	if !common.IsHexAddress(from) {
		return TransferTx{}, fmt.Errorf("invalid sender address")
	}
	target := recipient
	value := amount
	data := []byte(nil)
	if !isNativeToken(tokenAddress) {
		packed, err := PackERC20Transfer(recipient, amount)
		if err != nil {
			return TransferTx{}, err
		}
		target = tokenAddress
		value = big.NewInt(0)
		data = packed
	}
	if !common.IsHexAddress(target) {
		return TransferTx{}, fmt.Errorf("invalid recipient address")
	}
	return TransferTx{From: common.HexToAddress(from).Hex(), To: common.HexToAddress(target).Hex(), Value: value, Data: data, GasLimit: gasLimit}, nil
}

func isNativeToken(address string) bool {
	trimmed := strings.TrimSpace(address)
	return trimmed == "" || strings.EqualFold(trimmed, "0x0000000000000000000000000000000000000000")
}

// SignTx signs txData with privateKey for chainID using EIP-1559 or legacy fees.
func SignTx(privateKey []byte, chainID int64, nonce uint64, tx TransferTx, fees *FeeEstimate, eip1559 bool) (*types.Transaction, error) {
	key, err := crypto.ToECDSA(privateKey)
	if err != nil {
		return nil, fmt.Errorf("invalid private key")
	}
	if chainID <= 0 {
		return nil, fmt.Errorf("chain id is required")
	}
	if eip1559 {
		fees = EnsureEIP1559(fees)
		if fees == nil || fees.MaxFeePerGas == nil || fees.MaxPriorityFeePerGas == nil {
			return nil, fmt.Errorf("eip-1559 fees are required")
		}
	}
	var signed *types.Transaction
	if eip1559 {
		signed, err = types.SignNewTx(key, types.NewLondonSigner(big.NewInt(chainID)), &types.DynamicFeeTx{
			ChainID:   big.NewInt(chainID),
			Nonce:     nonce,
			GasTipCap: fees.MaxPriorityFeePerGas,
			GasFeeCap: fees.MaxFeePerGas,
			Gas:       tx.GasLimit,
			To:        addrPtr(tx.To),
			Value:     tx.Value,
			Data:      tx.Data,
		})
	} else {
		price := big.NewInt(0)
		if fees != nil && fees.GasPrice != nil {
			price = fees.GasPrice
		}
		signed, err = types.SignNewTx(key, types.NewEIP155Signer(big.NewInt(chainID)), &types.LegacyTx{
			Nonce:    nonce,
			GasPrice: price,
			Gas:      tx.GasLimit,
			To:       addrPtr(tx.To),
			Value:    tx.Value,
			Data:     tx.Data,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("sign transaction: %w", err)
	}
	return signed, nil
}

func addrPtr(address string) *common.Address {
	value := common.HexToAddress(address)
	return &value
}

// BroadcastTx submits a signed transaction and returns its hash.
func (c *Client) BroadcastTx(ctx context.Context, rpc string, signed *types.Transaction) (string, error) {
	localHash := signed.Hash().Hex()
	raw, err := signed.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("encode transaction: %w", err)
	}
	payload, err := json.Marshal(request{JSONRPC: "2.0", ID: 1, Method: "eth_sendRawTransaction", Params: []any{"0x" + fmt.Sprintf("%x", raw)}})
	if err != nil {
		return "", fmt.Errorf("encode broadcast: %w", err)
	}
	res, err := c.post(ctx, rpc, payload)
	if err != nil {
		return "", err
	}
	var decoded rawResponse
	if err := json.Unmarshal(res, &decoded); err != nil {
		return "", fmt.Errorf("decode broadcast response: %w", err)
	}
	if decoded.Error != nil {
		noteJSONRPC(ctx, decoded.Error)
		if acceptedBroadcast(decoded.Error.Message) {
			return localHash, nil
		}
		return "", fmt.Errorf("broadcast: %s", decoded.Error.Message)
	}
	var hash string
	if err := json.Unmarshal(decoded.Result, &hash); err != nil {
		return "", fmt.Errorf("decode tx hash: %w", err)
	}
	if hash == "" {
		return localHash, nil
	}
	return hash, nil
}

func acceptedBroadcast(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "already known") || strings.Contains(lower, "known transaction") || strings.Contains(lower, "already imported")
}

// Allowance reads ERC20 allowance(owner, spender) via eth_call.
func (c *Client) Allowance(ctx context.Context, rpc string, tokenAddress string, owner string, spender string) (*big.Int, error) {
	packedABI, err := erc20ABI()
	if err != nil {
		return nil, err
	}
	data, err := packedABI.Pack("allowance", common.HexToAddress(owner), common.HexToAddress(spender))
	if err != nil {
		return nil, fmt.Errorf("pack allowance: %w", err)
	}
	call := map[string]string{"to": common.HexToAddress(tokenAddress).Hex(), "data": "0x" + fmt.Sprintf("%x", data)}
	result, err := c.callRaw(ctx, rpc, "eth_call", []any{call, "latest"})
	if err != nil {
		return nil, err
	}
	var encoded string
	if err := json.Unmarshal(result, &encoded); err != nil {
		return nil, fmt.Errorf("decode allowance: %w", err)
	}
	value, err := parseHex(encoded)
	if err != nil {
		return nil, fmt.Errorf("parse allowance: %w", err)
	}
	return value, nil
}

// EstimateGasLimit reads eth_estimateGas for a transfer-shaped call.
func (c *Client) EstimateGasLimit(ctx context.Context, rpc string, tx TransferTx) (uint64, error) {
	call, err := callParams(tx)
	if err != nil {
		return 0, err
	}
	result, err := c.callRaw(ctx, rpc, "eth_estimateGas", []any{call})
	if err != nil {
		return 0, err
	}
	var encoded string
	if err := json.Unmarshal(result, &encoded); err != nil {
		return 0, fmt.Errorf("decode gas estimate: %w", err)
	}
	value, err := parseHex(encoded)
	if err != nil {
		return 0, fmt.Errorf("parse gas estimate: %w", err)
	}
	if !value.IsUint64() || value.Uint64() == 0 {
		return 0, fmt.Errorf("gas estimate out of range")
	}
	return value.Uint64(), nil
}
