package transfer

import (
	"errors"
	"math/big"
	"testing"

	"multi-wallet-cli/infra/logcsv"
)

func TestClassifyOutcome_Success(t *testing.T) {
	status, code := classifyOutcome(lineOutcome{txHash: "0x1", status: 1})
	if status != logcsv.StatusSuccess || code != "" {
		t.Fatalf("expected success with empty code, got %s/%s", status, code)
	}
}

func TestClassifyOutcome_Reverted(t *testing.T) {
	status, code := classifyOutcome(lineOutcome{txHash: "0x1", status: 0, block: big.NewInt(123), err: errors.New("reverted on-chain")})
	if status != logcsv.StatusReverted || code != logcsv.ErrReverted {
		t.Fatalf("expected reverted/REVERTED, got %s/%s", status, code)
	}
}

func TestClassifyOutcome_SkippedBeforeBroadcast(t *testing.T) {
	status, code := classifyOutcome(lineOutcome{err: errors.New("insufficient funds for gas")})
	if status != logcsv.StatusSkipped || code != logcsv.ErrInsufficientFunds {
		t.Fatalf("expected skipped/INSUFFICIENT_FUNDS, got %s/%s", status, code)
	}
}

func TestClassifyOutcome_SubmittedOnTimeout(t *testing.T) {
	status, code := classifyOutcome(lineOutcome{txHash: "0x1", err: errors.New("receipt timeout")})
	if status != logcsv.StatusSubmitted || code != logcsv.ErrRPCTimeout {
		t.Fatalf("expected submitted/RPC_TIMEOUT, got %s/%s", status, code)
	}
}

func TestFeeNative_MultipliesGasByPrice(t *testing.T) {
	fee := feeNative(lineOutcome{gasUsed: big.NewInt(21000), effectiveGasPrice: big.NewInt(50)})
	if fee != "1050000" {
		t.Fatalf("expected 1050000, got %s", fee)
	}
}

func TestFeeNative_ZeroWhenMissing(t *testing.T) {
	if feeNative(lineOutcome{}) != "0" {
		t.Fatal("expected 0 when gas fields are missing")
	}
}

func TestNormalizeTransferError_Codes(t *testing.T) {
	cases := map[string]string{
		"insufficient funds for intrinsic transaction cost":   logcsv.ErrInsufficientFunds,
		"execution reverted: transfer amount exceeds balance": logcsv.ErrReverted,
		"receipt timeout":          logcsv.ErrRPCTimeout,
		"nonce too low":            logcsv.ErrReplaced,
		"signing key unavailable":  logcsv.ErrSigningFailed,
		"broadcast: already known": logcsv.ErrBroadcastFailed,
		"something odd happened":   logcsv.ErrUnknown,
	}
	for message, expected := range cases {
		if got := normalizeTransferError(errors.New(message)); got != expected {
			t.Fatalf("message %q expected %s got %s", message, expected, got)
		}
	}
}

func TestNewBatchID_UniqueAndPrefixed(t *testing.T) {
	first := newBatchID()
	second := newBatchID()
	if first == second {
		t.Fatal("expected unique batch ids")
	}
	if first[:6] != "batch-" {
		t.Fatalf("expected batch- prefix, got %s", first)
	}
}
