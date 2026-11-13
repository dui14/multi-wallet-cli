package cli

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestParseAddressList_SplitsSpaceCommaAndNewline(t *testing.T) {
	raw := "0x00000000000000000000000000000000000000a1 0x00000000000000000000000000000000000000a2,0x00000000000000000000000000000000000000a3\n0x00000000000000000000000000000000000000a4"
	got := parseAddressList(raw)
	if len(got) != 4 {
		t.Fatalf("expected 4 addresses, got %#v", got)
	}
}

func TestParseAddressList_EmptyIsEmpty(t *testing.T) {
	if len(parseAddressList("  \n ")) != 0 {
		t.Fatal("blank paste should produce no addresses")
	}
}

func TestDestinationKey_UsesChecksum(t *testing.T) {
	raw := "0xfd9e762a5f3d69d84a0509e3926e901525ea7455"
	key := destinationKey(raw)
	want := common.HexToAddress(raw).Hex()
	if key != want {
		t.Fatalf("checksum key = %s, want %s", key, want)
	}
}
