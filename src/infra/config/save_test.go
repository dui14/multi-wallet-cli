package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const sampleNetworks = `{
  "_metadata": { "verifiedAt": "2026-10-07", "sources": ["https://example.com"] },
  "ethereum": {
    "name": "Ethereum Mainnet",
    "vmType": "evm",
    "chainId": 1,
    "relayChainId": 1,
    "nativeSymbol": "ETH",
    "nativeDecimals": 18,
    "eip1559": true,
    "seedRpcs": ["https://ethereum-rpc.publicnode.com"],
    "explorer": "https://etherscan.io/"
  }
}`

const sampleTokens = `{
  "_metadata": { "verifiedAt": "2026-10-07" },
  "ethereum": {
    "USDC": { "address": "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", "decimals": 6, "symbol": "USDC" }
  }
}`

func TestSaveNetwork_PreservesMetadataAndExisting(t *testing.T) {
	baseDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseDir, "networks.json"), []byte(sampleNetworks), 0o600); err != nil {
		t.Fatal(err)
	}
	added := Network{
		Name:           "Linea Mainnet",
		VMType:         "evm",
		ChainID:        59144,
		RelayChainID:   59144,
		NativeSymbol:   "ETH",
		NativeDecimals: 18,
		EIP1559:        true,
		SeedRPCs:       []string{"https://rpc.linea.build"},
	}
	if err := SaveNetwork(baseDir, "linea", added); err != nil {
		t.Fatal(err)
	}
	networks, err := LoadNetworks(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := networks["ethereum"]; !ok {
		t.Fatal("existing network was lost")
	}
	linea, ok := networks["linea"]
	if !ok {
		t.Fatal("added network missing")
	}
	if linea.ChainID != 59144 || linea.RelayChainID != 59144 {
		t.Fatalf("linea chain fields wrong: %#v", linea)
	}
	raw, err := os.ReadFile(filepath.Join(baseDir, "networks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if _, ok := document["_metadata"]; !ok {
		t.Fatal("_metadata block was dropped")
	}
}

func TestSaveToken_PreservesMetadataAndExisting(t *testing.T) {
	baseDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseDir, "tokens.json"), []byte(sampleTokens), 0o600); err != nil {
		t.Fatal(err)
	}
	added := Token{Address: "0x503828976D22510aad0201ac7EC88293211D23Da", Decimals: 18, Symbol: "DRV"}
	if err := SaveToken(baseDir, "ethereum", "DRV", added); err != nil {
		t.Fatal(err)
	}
	tokens, err := LoadTokens(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tokens["ethereum"]["USDC"]; !ok {
		t.Fatal("existing token was lost")
	}
	drv, ok := tokens["ethereum"]["DRV"]
	if !ok {
		t.Fatal("added token missing")
	}
	if drv.Address != added.Address || drv.Decimals != 18 {
		t.Fatalf("drv token wrong: %#v", drv)
	}
	raw, err := os.ReadFile(filepath.Join(baseDir, "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if _, ok := document["_metadata"]; !ok {
		t.Fatal("_metadata block was dropped")
	}
}

func TestSaveNetwork_UpdatesExistingEntry(t *testing.T) {
	baseDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseDir, "networks.json"), []byte(sampleNetworks), 0o600); err != nil {
		t.Fatal(err)
	}
	updated := Network{Name: "Ethereum Mainnet", VMType: "evm", ChainID: 1, RelayChainID: 1, NativeSymbol: "ETH", NativeDecimals: 18, SeedRPCs: []string{"https://eth.llamarpc.com"}}
	if err := SaveNetwork(baseDir, "ethereum", updated); err != nil {
		t.Fatal(err)
	}
	networks, err := LoadNetworks(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	if networks["ethereum"].SeedEndpoints()[0] != "https://eth.llamarpc.com" {
		t.Fatalf("network not updated: %#v", networks["ethereum"])
	}
}
