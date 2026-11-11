package token

import (
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/mr-tron/base58"
)

// Token is a validated transfer target: known symbol, native sentinel, or custom CA/mint.
type Token struct {
	Symbol   string
	Address  string
	Decimals uint8
	Native   bool
	Custom   bool
}

// Resolve maps symbol or custom address to a Token using the registry entry.
func Resolve(chain string, symbol string, custom string, registry map[string]Token, nativeSymbol string, nativeDecimals uint8, nativeAddress string) (Token, error) {
	trimmedCustom := strings.TrimSpace(custom)
	if trimmedCustom != "" {
		if common.IsHexAddress(trimmedCustom) {
			return Token{Symbol: "CUSTOM", Address: common.HexToAddress(trimmedCustom).Hex(), Custom: true}, nil
		}
		if _, err := base58.Decode(trimmedCustom); err == nil {
			return Token{Symbol: "CUSTOM", Address: trimmedCustom, Custom: true}, nil
		}
		return Token{}, fmt.Errorf("custom contract is not a valid address")
	}
	trimmed := strings.ToUpper(strings.TrimSpace(symbol))
	if trimmed == "" {
		return Token{}, fmt.Errorf("token is required")
	}
	if trimmed == "NATIVE" || trimmed == strings.ToUpper(nativeSymbol) {
		return Token{Symbol: nativeSymbol, Address: nativeAddress, Decimals: nativeDecimals, Native: true}, nil
	}
	if registry != nil {
		if entry, ok := registry[trimmed]; ok {
			return entry, nil
		}
	}
	return Token{}, fmt.Errorf("unknown token %q on %s", symbol, chain)
}

// IsNativeAddress reports the EVM native sentinel address.
func IsNativeAddress(address string) bool {
	return strings.EqualFold(strings.TrimSpace(address), "0x0000000000000000000000000000000000000000")
}
