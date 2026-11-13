package amount

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

var (
	ErrNonPositive          = errors.New("amount must be greater than zero")
	ErrBalanceUnavailable   = errors.New("balance is unavailable")
	ErrNotBelowBalance      = errors.New("amount must be less than the balance")
	ErrTotalNotBelowBalance = errors.New("total amount must be less than the balance")
)

// Amount is a token quantity in base units with its display decimals.
type Amount struct {
	Raw      *big.Int
	Decimals uint8
}

// Parse converts a human string such as "1.5" into base units.
func Parse(human string, decimals uint8) (*big.Int, error) {
	trimmed := strings.TrimSpace(human)
	if trimmed == "" {
		return nil, fmt.Errorf("amount is required")
	}
	if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "+") {
		return nil, fmt.Errorf("amount must be positive")
	}
	parts := strings.SplitN(trimmed, ".", 3)
	if len(parts) > 2 {
		return nil, fmt.Errorf("amount has too many decimal points")
	}
	whole := parts[0]
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	if whole == "" && frac == "" {
		return nil, fmt.Errorf("amount is required")
	}
	for _, ch := range whole + frac {
		if ch < '0' || ch > '9' {
			return nil, fmt.Errorf("amount must contain digits only")
		}
	}
	if len(frac) > int(decimals) {
		return nil, fmt.Errorf("amount exceeds %d decimals", decimals)
	}
	frac += strings.Repeat("0", int(decimals)-len(frac))
	digits := strings.TrimLeft(whole+frac, "0")
	if digits == "" {
		return nil, fmt.Errorf("amount must be greater than zero")
	}
	raw := new(big.Int)
	if _, ok := raw.SetString(digits, 10); !ok {
		return nil, fmt.Errorf("amount is not a number")
	}
	if raw.Sign() <= 0 {
		return nil, fmt.Errorf("amount must be greater than zero")
	}
	return raw, nil
}

// LessThanBalance reports whether value is a positive quantity strictly below balance.
func LessThanBalance(value *big.Int, balance *big.Int) error {
	if value == nil || value.Sign() <= 0 {
		return ErrNonPositive
	}
	if balance == nil {
		return ErrBalanceUnavailable
	}
	if value.Cmp(balance) >= 0 {
		return ErrNotBelowBalance
	}
	return nil
}

// TotalLessThanBalance reports whether every value is positive and the sum is strictly below balance.
func TotalLessThanBalance(values []*big.Int, balance *big.Int) error {
	if balance == nil {
		return ErrBalanceUnavailable
	}
	total := big.NewInt(0)
	for _, value := range values {
		if err := LessThanBalance(value, balance); err != nil {
			return err
		}
		total.Add(total, value)
	}
	if total.Sign() <= 0 {
		return ErrNonPositive
	}
	if total.Cmp(balance) >= 0 {
		return ErrTotalNotBelowBalance
	}
	return nil
}

// RepeatedLessThanBalance reports whether repeating value count times stays strictly below balance.
func RepeatedLessThanBalance(value *big.Int, count int, balance *big.Int) error {
	if count <= 0 {
		return ErrNonPositive
	}
	values := make([]*big.Int, count)
	for index := range values {
		values[index] = value
	}
	return TotalLessThanBalance(values, balance)
}

// Format renders base units as a human string without float math.
func Format(raw *big.Int, decimals uint8) string {
	if raw == nil {
		return "-"
	}
	digits := raw.Text(10)
	if int(decimals) == 0 {
		return digits
	}
	if len(digits) <= int(decimals) {
		digits = strings.Repeat("0", int(decimals)-len(digits)+1) + digits
	}
	whole := digits[:len(digits)-int(decimals)]
	frac := strings.TrimRight(digits[len(digits)-int(decimals):], "0")
	if frac == "" {
		return whole
	}
	return whole + "." + frac
}
