package amount

import (
	"math/big"
	"testing"
)

func TestLessThanBalance_RejectsZeroEqualAndAbove(t *testing.T) {
	balance := big.NewInt(100)
	if err := LessThanBalance(big.NewInt(0), balance); err != ErrNonPositive {
		t.Fatalf("zero: got %v", err)
	}
	if err := LessThanBalance(big.NewInt(100), balance); err != ErrNotBelowBalance {
		t.Fatalf("equal: got %v", err)
	}
	if err := LessThanBalance(big.NewInt(101), balance); err != ErrNotBelowBalance {
		t.Fatalf("above: got %v", err)
	}
	if err := LessThanBalance(big.NewInt(99), balance); err != nil {
		t.Fatalf("below: got %v", err)
	}
	if err := LessThanBalance(big.NewInt(1), nil); err != ErrBalanceUnavailable {
		t.Fatalf("missing balance: got %v", err)
	}
}

func TestTotalLessThanBalance_RejectsSumEqualToBalance(t *testing.T) {
	balance := big.NewInt(100)
	err := TotalLessThanBalance([]*big.Int{big.NewInt(40), big.NewInt(60)}, balance)
	if err != ErrTotalNotBelowBalance {
		t.Fatalf("sum equal: got %v", err)
	}
	if err := TotalLessThanBalance([]*big.Int{big.NewInt(40), big.NewInt(59)}, balance); err != nil {
		t.Fatalf("sum below: got %v", err)
	}
}

func TestRepeatedLessThanBalance_ScalesByCount(t *testing.T) {
	balance := big.NewInt(100)
	if err := RepeatedLessThanBalance(big.NewInt(50), 2, balance); err != ErrTotalNotBelowBalance {
		t.Fatalf("two copies: got %v", err)
	}
	if err := RepeatedLessThanBalance(big.NewInt(40), 1, balance); err != nil {
		t.Fatalf("one copy: got %v", err)
	}
}
