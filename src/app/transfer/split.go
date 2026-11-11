package transfer

import (
	"context"
	"fmt"
	"math/big"

	"multi-wallet-cli/domain/amount"
)

const gasBufferPct = 20

func bufferGas(gasLimit uint64) uint64 {
	return gasLimit + gasLimit*gasBufferPct/100
}

func (f *Flow) blockGasLimit(ctx context.Context) uint64 {
	if f.blockGas > 0 {
		return f.blockGas
	}
	var limit uint64
	if err := f.call(ctx, func(callCtx context.Context, rpcURL string) error {
		var blockErr error
		limit, blockErr = f.evm.BlockGasLimit(callCtx, rpcURL)
		return blockErr
	}); err != nil {
		return 0
	}
	f.blockGas = limit
	return limit
}

func formatUnits(raw *big.Int, decimals uint8) string {
	return amount.Format(raw, decimals)
}

func splitAmounts(item TokenLine, recipients []string, mode SplitMode) ([]*big.Int, error) {
	if len(recipients) == 0 {
		return nil, fmt.Errorf("no recipients to split across")
	}
	switch mode {
	case SplitPerRecipient:
		out := make([]*big.Int, len(recipients))
		for index, recipient := range recipients {
			value, ok := item.PerRecipient[normalizeEVMAddress(recipient)]
			if !ok {
				return nil, fmt.Errorf("missing amount for recipient %s", recipient)
			}
			out[index] = new(big.Int).Set(value)
		}
		return out, nil
	default:
		if item.Amount == nil {
			return nil, fmt.Errorf("amount is required for %s", item.Symbol)
		}
		out := make([]*big.Int, len(recipients))
		for index := range out {
			out[index] = new(big.Int).Set(item.Amount)
		}
		return out, nil
	}
}

func distributeEven(total *big.Int, count int) []*big.Int {
	base := new(big.Int).Div(total, big.NewInt(int64(count)))
	remainder := new(big.Int).Mod(total, big.NewInt(int64(count)))
	out := make([]*big.Int, count)
	for index := range out {
		value := new(big.Int).Set(base)
		if int64(index) < remainder.Int64() {
			value.Add(value, big.NewInt(1))
		}
		out[index] = value
	}
	return out
}
