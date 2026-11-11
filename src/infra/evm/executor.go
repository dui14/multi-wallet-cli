package evm

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/holiman/uint256"
)

// ErrExecutorCodeMismatch means the pinned executor hash does not match eth_getCode.
var ErrExecutorCodeMismatch = errors.New("executor code hash mismatch")

// Call is one entry inside execute(calls).
type Call struct {
	To    common.Address
	Value *big.Int
	Data  []byte
}

const executeABIJSON = `[{"type":"function","name":"execute","stateMutability":"payable","inputs":[{"name":"calls","type":"tuple[]","components":[{"name":"to","type":"address"},{"name":"value","type":"uint256"},{"name":"data","type":"bytes"}]}],"outputs":[]}]`

var parsedExecuteABI abi.ABI

func executeABI() (abi.ABI, error) {
	if len(parsedExecuteABI.Methods) != 0 {
		return parsedExecuteABI, nil
	}
	parsed, err := abi.JSON(strings.NewReader(executeABIJSON))
	if err != nil {
		return abi.ABI{}, err
	}
	parsedExecuteABI = parsed
	return parsed, nil
}

type abiCall struct {
	To    common.Address `abi:"to"`
	Value *big.Int       `abi:"value"`
	Data  []byte         `abi:"data"`
}

// PackExecute encodes execute(calls) for the pinned executor.
func PackExecute(calls []Call) ([]byte, error) {
	if len(calls) == 0 {
		return nil, fmt.Errorf("execute requires at least one call")
	}
	args := make([]abiCall, len(calls))
	for index, call := range calls {
		if call.To == (common.Address{}) {
			return nil, fmt.Errorf("call %d is missing a target", index+1)
		}
		value := call.Value
		if value == nil {
			value = big.NewInt(0)
		}
		if value.Sign() < 0 {
			return nil, fmt.Errorf("call %d value must not be negative", index+1)
		}
		args[index] = abiCall{To: call.To, Value: value, Data: call.Data}
	}
	packedABI, err := executeABI()
	if err != nil {
		return nil, err
	}
	return packedABI.Pack("execute", args)
}

// AuthorizationForOwner builds an unsigned authorization whose nonce is txNonce+1.
func AuthorizationForOwner(chainID int64, executor string, txNonce uint64) (types.SetCodeAuthorization, error) {
	if chainID <= 0 {
		return types.SetCodeAuthorization{}, fmt.Errorf("authorization chain id is required")
	}
	if !common.IsHexAddress(executor) {
		return types.SetCodeAuthorization{}, fmt.Errorf("invalid executor address")
	}
	return types.SetCodeAuthorization{
		ChainID: *uint256.MustFromBig(big.NewInt(chainID)),
		Address: common.HexToAddress(executor),
		Nonce:   txNonce + 1,
	}, nil
}

// SignSetCodeTx signs a type-4 transaction. The same key signs the authorization,
// so the authorization nonce must be the transaction nonce plus one.
func SignSetCodeTx(privateKey []byte, chainID int64, nonce uint64, tx TransferTx, fees *FeeEstimate, auth types.SetCodeAuthorization) (*types.Transaction, error) {
	if chainID <= 0 || auth.ChainID.IsZero() {
		return nil, fmt.Errorf("authorization chain id is required")
	}
	if auth.Nonce != nonce+1 {
		return nil, fmt.Errorf("authorization nonce must be the transaction nonce plus one")
	}
	key, err := crypto.ToECDSA(privateKey)
	if err != nil {
		return nil, fmt.Errorf("invalid private key")
	}
	fees = EnsureEIP1559(fees)
	if fees == nil || fees.MaxFeePerGas == nil || fees.MaxPriorityFeePerGas == nil {
		return nil, fmt.Errorf("eip-1559 fees are required")
	}
	signedAuth, err := types.SignSetCode(key, auth)
	if err != nil {
		return nil, fmt.Errorf("sign authorization: %w", err)
	}
	value := big.NewInt(0)
	if tx.Value != nil {
		value = tx.Value
	}
	signed, err := types.SignNewTx(key, types.NewPragueSigner(big.NewInt(chainID)), &types.SetCodeTx{
		ChainID:   uint256.MustFromBig(big.NewInt(chainID)),
		Nonce:     nonce,
		GasTipCap: uint256.MustFromBig(fees.MaxPriorityFeePerGas),
		GasFeeCap: uint256.MustFromBig(fees.MaxFeePerGas),
		Gas:       tx.GasLimit,
		To:        common.HexToAddress(tx.To),
		Value:     uint256.MustFromBig(value),
		Data:      tx.Data,
		AuthList:  []types.SetCodeAuthorization{signedAuth},
	})
	if err != nil {
		return nil, fmt.Errorf("sign set-code transaction: %w", err)
	}
	return signed, nil
}

// DelegatedTo reports whether code is the pinned executor runtime or its delegation designator.
func DelegatedTo(code []byte, executor common.Address, runtimeHash common.Hash) bool {
	if len(code) == 0 || runtimeHash == (common.Hash{}) {
		return false
	}
	if crypto.Keccak256Hash(code) == runtimeHash {
		return true
	}
	target, ok := types.ParseDelegation(code)
	return ok && target == executor
}

// ExecutorCodeMatches compares eth_getCode bytes with the pinned keccak256 hash.
func ExecutorCodeMatches(code []byte, pinned string) error {
	pinned = strings.TrimSpace(pinned)
	if pinned == "" {
		return fmt.Errorf("executor code hash is required")
	}
	if len(code) == 0 || crypto.Keccak256Hash(code) != common.HexToHash(pinned) {
		return ErrExecutorCodeMismatch
	}
	return nil
}

// Code reads eth_getCode. Empty code returns a nil slice.
func (c *Client) Code(ctx context.Context, rpc, address string) ([]byte, error) {
	result, err := c.callRaw(ctx, rpc, "eth_getCode", []any{address, "latest"})
	if err != nil {
		return nil, err
	}
	var encoded string
	if err := json.Unmarshal(result, &encoded); err != nil {
		return nil, fmt.Errorf("decode code: %w", err)
	}
	trimmed := strings.TrimPrefix(strings.TrimPrefix(encoded, "0x"), "0X")
	if trimmed == "" || strings.Trim(trimmed, "0") == "" {
		return nil, nil
	}
	if len(trimmed)%2 == 1 {
		trimmed = "0" + trimmed
	}
	decoded, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("decode code: %w", err)
	}
	return decoded, nil
}

// SupportsSetCodeTx reports whether the latest block carries a Prague requestsHash.
func (c *Client) SupportsSetCodeTx(ctx context.Context, rpc string) (bool, error) {
	result, err := c.callRaw(ctx, rpc, "eth_getBlockByNumber", []any{"latest", false})
	if err != nil {
		return false, err
	}
	var block map[string]json.RawMessage
	if err := json.Unmarshal(result, &block); err != nil {
		return false, fmt.Errorf("decode block: %w", err)
	}
	raw, ok := block["requestsHash"]
	if !ok {
		return false, nil
	}
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null", nil
}

func callParams(tx TransferTx) (map[string]any, error) {
	call := map[string]any{}
	if strings.TrimSpace(tx.From) != "" {
		call["from"] = tx.From
	}
	if strings.TrimSpace(tx.To) != "" {
		call["to"] = tx.To
	}
	if tx.Value != nil {
		call["value"] = "0x" + tx.Value.Text(16)
	}
	if len(tx.Data) > 0 {
		call["data"] = "0x" + hex.EncodeToString(tx.Data)
	}
	if tx.Auth != nil {
		if tx.Auth.ChainID.IsZero() {
			return nil, fmt.Errorf("authorization chain id is required")
		}
		call["authorizationList"] = []types.SetCodeAuthorization{*tx.Auth}
	}
	return call, nil
}
