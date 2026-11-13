package transfer

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/common"

	"multi-wallet-cli/infra/evm"
)

func normalizeEVMAddress(value string) string {
	trimmed := strings.TrimSpace(value)
	if !common.IsHexAddress(trimmed) {
		return ""
	}
	return common.HexToAddress(trimmed).Hex()
}

func (f *Flow) requireRecipientEVM(value string) error {
	if normalizeEVMAddress(value) == "" {
		return fmt.Errorf("invalid EVM destination wallet address")
	}
	return nil
}

func (f *Flow) requireRecipientsEVM(values []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := normalizeEVMAddress(value)
		if normalized == "" {
			return nil, fmt.Errorf("invalid destination wallet address %q", value)
		}
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("select at least one destination wallet")
	}
	return out, nil
}

func (f *Flow) loadMeta(ctx context.Context, item TokenLine) (TokenLine, error) {
	if item.Native {
		item.Address = ""
		item.Decimals = f.NativeDecimals()
		return item, nil
	}
	address := normalizeEVMAddress(item.Address)
	if address == "" {
		return TokenLine{}, fmt.Errorf("invalid token address for %s", item.Symbol)
	}
	if item.Symbol != "" && item.Decimals > 0 {
		item.Address = address
		return item, nil
	}
	hasCode := false
	if err := f.call(ctx, func(callCtx context.Context, rpcURL string) error {
		var codeErr error
		hasCode, codeErr = f.evm.HasCode(callCtx, rpcURL, address)
		return codeErr
	}); err != nil {
		return TokenLine{}, err
	}
	if !hasCode {
		return TokenLine{}, fmt.Errorf("address %s has no contract code", address)
	}
	var symbol string
	if err := f.call(ctx, func(callCtx context.Context, rpcURL string) error {
		var symbolErr error
		symbol, symbolErr = f.evm.ReadSymbol(callCtx, rpcURL, address)
		return symbolErr
	}); err != nil {
		return TokenLine{}, err
	}
	var decimals uint8
	if err := f.call(ctx, func(callCtx context.Context, rpcURL string) error {
		var decimalsErr error
		decimals, decimalsErr = f.evm.ReadDecimals(callCtx, rpcURL, address)
		return decimalsErr
	}); err != nil {
		return TokenLine{}, err
	}
	name := ""
	if err := f.call(ctx, func(callCtx context.Context, rpcURL string) error {
		var nameErr error
		name, nameErr = f.evm.ReadName(callCtx, rpcURL, address)
		return nameErr
	}); err != nil {
		name = ""
	}
	return TokenLine{
		Symbol:       symbol,
		Name:         name,
		Address:      address,
		Decimals:     decimals,
		Custom:       true,
		Unverified:   true,
		Amount:       item.Amount,
		Max:          item.Max,
		Keep:         item.Keep,
		PerRecipient: item.PerRecipient,
	}, nil
}

func (f *Flow) balanceOf(ctx context.Context, owner string, item TokenLine) (*big.Int, error) {
	key := owner + ":" + item.Address
	f.mu.Lock()
	cached, ok := f.balances[key]
	f.mu.Unlock()
	if ok {
		return cached, nil
	}
	var value *big.Int
	err := f.call(ctx, func(callCtx context.Context, rpcURL string) error {
		var balanceErr error
		if item.Native {
			value, balanceErr = f.evm.NativeBalance(callCtx, rpcURL, owner)
		} else {
			value, balanceErr = f.evm.TokenBalance(callCtx, rpcURL, owner, item.Address)
		}
		return balanceErr
	})
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.balances[key] = value
	f.mu.Unlock()
	return value, nil
}

func (f *Flow) resolveAmount(item TokenLine, balance *big.Int) (*big.Int, error) {
	if item.Max {
		return new(big.Int).Set(balance), nil
	}
	if item.Amount == nil {
		return nil, fmt.Errorf("amount is required for %s", item.Symbol)
	}
	if item.Amount.Cmp(balance) > 0 {
		return nil, fmt.Errorf("%s: amount exceeds balance", item.Symbol)
	}
	return new(big.Int).Set(item.Amount), nil
}

func (f *Flow) nonce(ctx context.Context, address string) (uint64, error) {
	f.mu.Lock()
	cached, ok := f.nonces[address]
	f.mu.Unlock()
	if ok {
		return cached, nil
	}
	var value uint64
	if err := f.call(ctx, func(callCtx context.Context, rpcURL string) error {
		var nonceErr error
		value, nonceErr = f.evm.PendingNonce(callCtx, rpcURL, address)
		return nonceErr
	}); err != nil {
		return 0, err
	}
	f.mu.Lock()
	f.nonces[address] = value
	f.mu.Unlock()
	return value, nil
}

func (f *Flow) estimateFees(ctx context.Context) (*evm.FeeEstimate, error) {
	f.mu.Lock()
	cached := f.fees
	f.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	estimate, err := f.collectFees(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.fees = estimate
	f.mu.Unlock()
	return estimate, nil
}

func (f *Flow) estimateFeesForSend(ctx context.Context) (*evm.FeeEstimate, error) {
	return f.collectFees(ctx)
}

func (f *Flow) collectFees(ctx context.Context) (*evm.FeeEstimate, error) {
	urls := f.liveRPCs()
	if len(urls) == 0 {
		return nil, fmt.Errorf("no rpc endpoints")
	}
	estimates := make([]*evm.FeeEstimate, len(urls))
	var wg sync.WaitGroup
	for index, rpcURL := range urls {
		wg.Add(1)
		go func(index int, rpcURL string) {
			defer wg.Done()
			pollCtx, cancel := context.WithTimeout(ctx, receiptCallTimeout)
			defer cancel()
			estimate, err := f.evm.EstimateFees(pollCtx, rpcURL)
			if err != nil {
				return
			}
			estimates[index] = estimate
		}(index, rpcURL)
	}
	wg.Wait()
	merged := evm.MergeFeeEstimates(estimates)
	if merged == nil || (merged.MaxFeePerGas == nil && merged.GasPrice == nil) {
		return nil, fmt.Errorf("fee estimate unavailable")
	}
	return merged, nil
}

func feeCap(fees *evm.FeeEstimate, eip1559 bool) *big.Int {
	if fees == nil {
		return big.NewInt(0)
	}
	if eip1559 && fees.MaxFeePerGas != nil {
		return fees.MaxFeePerGas
	}
	if fees.GasPrice != nil {
		return fees.GasPrice
	}
	if fees.MaxFeePerGas != nil {
		return fees.MaxFeePerGas
	}
	return big.NewInt(0)
}

func (f *Flow) feePrice(ctx context.Context) *big.Int {
	estimate, err := f.estimateFees(ctx)
	if err != nil {
		return big.NewInt(0)
	}
	if f.EIP1559() && estimate.MaxFeePerGas != nil {
		return estimate.MaxFeePerGas
	}
	if estimate.GasPrice != nil {
		return estimate.GasPrice
	}
	return big.NewInt(0)
}
