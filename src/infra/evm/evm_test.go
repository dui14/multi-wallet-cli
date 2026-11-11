package evm

import "testing"

func TestParseHex_QuantityZero(t *testing.T) {
	value, err := parseHex("0x0")
	if err != nil {
		t.Fatal(err)
	}
	if value.Sign() != 0 {
		t.Fatalf("expected zero, got %s", value)
	}
}
