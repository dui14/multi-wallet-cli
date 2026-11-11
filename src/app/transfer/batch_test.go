package transfer

import (
	"math/big"
	"testing"
)

func TestEnterMax_NativeReservesBatchGasOnce(t *testing.T) {
	balance := big.NewInt(1_000_000)
	gasCost := big.NewInt(90_000)
	parts, err := assignNativeMax(balance, gasCost, big.NewInt(0), big.NewInt(0), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 {
		t.Fatalf("expected 3 shares, got %d", len(parts))
	}
	sum := big.NewInt(0)
	for _, part := range parts {
		if part.Sign() <= 0 {
			t.Fatalf("share must be positive, got %s", part)
		}
		sum.Add(sum, part)
	}
	if sum.Cmp(new(big.Int).Sub(balance, gasCost)) != 0 {
		t.Fatalf("shares %s plus gas %s must equal balance %s", sum, gasCost, balance)
	}
	perLine := new(big.Int).Sub(balance, gasCost)
	if parts[0].Cmp(perLine) == 0 {
		t.Fatal("each line must share the remainder instead of taking the whole balance minus gas")
	}
}

func TestAssignNativeMax_SubtractsAssignedAndKeep(t *testing.T) {
	parts, err := assignNativeMax(big.NewInt(1000), big.NewInt(100), big.NewInt(50), big.NewInt(200), 1)
	if err != nil {
		t.Fatal(err)
	}
	if parts[0].Cmp(big.NewInt(650)) != 0 {
		t.Fatalf("expected 650, got %s", parts[0])
	}
}

func TestAssignNativeMax_RejectsNonPositive(t *testing.T) {
	if _, err := assignNativeMax(big.NewInt(100), big.NewInt(100), nil, nil, 1); err == nil {
		t.Fatal("expected gas to consume the balance")
	}
}

func TestSelectBatchPath_Table(t *testing.T) {
	cases := []struct {
		calls     int
		ready     bool
		delegated bool
		want      BatchPath
	}{
		{calls: 1, ready: true, delegated: true, want: PathOrdinary},
		{calls: 2, ready: true, delegated: false, want: PathOrdinary},
		{calls: 2, ready: true, delegated: true, want: PathSelfCall},
		{calls: 3, ready: true, delegated: false, want: PathSetCode},
		{calls: 4, ready: true, delegated: true, want: PathSelfCall},
		{calls: 5, ready: false, delegated: true, want: PathOrdinary},
	}
	for _, tc := range cases {
		got := selectBatchPath(tc.calls, tc.ready, tc.delegated, 3, 2)
		if got != tc.want {
			t.Fatalf("calls=%d ready=%v delegated=%v: got %s, want %s", tc.calls, tc.ready, tc.delegated, got.Label(), tc.want.Label())
		}
	}
}
