package transfer

import (
	"math/big"
	"testing"
)

func tokenLine(symbol string, amount int64) TokenLine {
	return TokenLine{Symbol: symbol, Address: "0x0000000000000000000000000000000000000001", Decimals: 18, Amount: big.NewInt(amount)}
}

func TestSplitAmounts_EqualRepeatsPerRecipient(t *testing.T) {
	recipients := []string{"0x00000000000000000000000000000000000000a1", "0x00000000000000000000000000000000000000a2", "0x00000000000000000000000000000000000000a3"}
	values, err := splitAmounts(tokenLine("USDC", 5), recipients, SplitSame)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(values) != len(recipients) {
		t.Fatalf("expected %d values, got %d", len(recipients), len(values))
	}
	for index, value := range values {
		if value.Cmp(big.NewInt(5)) != 0 {
			t.Fatalf("recipient %d expected 5, got %s", index, value.String())
		}
	}
}

func TestSplitAmounts_PerRecipientUsesProvidedMap(t *testing.T) {
	recipients := []string{"0x00000000000000000000000000000000000000a1", "0x00000000000000000000000000000000000000a2"}
	line := tokenLine("USDC", 0)
	line.PerRecipient = map[string]*big.Int{
		normalizeEVMAddress(recipients[0]): big.NewInt(7),
		normalizeEVMAddress(recipients[1]): big.NewInt(9),
	}
	values, err := splitAmounts(line, recipients, SplitPerRecipient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if values[0].Cmp(big.NewInt(7)) != 0 || values[1].Cmp(big.NewInt(9)) != 0 {
		t.Fatalf("unexpected per-recipient values: %s, %s", values[0].String(), values[1].String())
	}
}

func TestSplitAmounts_PerRecipientMissingAmountErrors(t *testing.T) {
	recipients := []string{"0x00000000000000000000000000000000000000a1", "0x00000000000000000000000000000000000000a2"}
	line := tokenLine("USDC", 0)
	line.PerRecipient = map[string]*big.Int{normalizeEVMAddress(recipients[0]): big.NewInt(7)}
	if _, err := splitAmounts(line, recipients, SplitPerRecipient); err == nil {
		t.Fatal("expected an error when a recipient amount is missing")
	}
}

func TestSplitAmounts_EqualRequiresAmount(t *testing.T) {
	recipients := []string{"0x00000000000000000000000000000000000000a1"}
	line := TokenLine{Symbol: "USDC", Address: "0x0000000000000000000000000000000000000001", Decimals: 18}
	if _, err := splitAmounts(line, recipients, SplitSame); err == nil {
		t.Fatal("expected an error when equal mode has no amount")
	}
}
