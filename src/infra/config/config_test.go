package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/mr-tron/base58"
)

func TestLoadTokens_IgnoresWarningMetadata(t *testing.T) {
	baseDir := t.TempDir()
	content := []byte(`{"_warning":"verify addresses","_metadata":{"verifiedAt":"2026-10-07"},"ethereum":{"USDC":{"address":"0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48","decimals":6,"symbol":"USDC"}}}`)
	if err := os.WriteFile(filepath.Join(baseDir, "tokens.json"), content, 0o600); err != nil {
		t.Fatal(err)
	}

	tokens, err := LoadTokens(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	if tokens["ethereum"]["USDC"].Address != "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48" {
		t.Fatalf("expected token address to load, got %#v", tokens)
	}
	if _, exists := tokens["_warning"]; exists {
		t.Fatal("warning metadata must not be returned as token data")
	}
	if _, exists := tokens["_metadata"]; exists {
		t.Fatal("file metadata must not be returned as token data")
	}
}

func TestRegistry_CrossFileConsistency(t *testing.T) {
	baseDir := findRepoBase(t)
	networks, err := LoadNetworks(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := LoadTokens(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	for chain, chainTokens := range tokens {
		network, ok := networks[chain]
		if !ok {
			t.Fatalf("tokens chain %q missing in networks.json", chain)
		}
		for symbol, token := range chainTokens {
			if strings.HasPrefix(symbol, "_") {
				t.Fatalf("unexpected metadata key %q in tokens for %s", symbol, chain)
			}
			if strings.TrimSpace(token.Address) == "" {
				t.Fatalf("token %s on %s missing address", symbol, chain)
			}
			if token.Symbol == "" {
				t.Fatalf("token %s on %s missing symbol", symbol, chain)
			}
			if token.Decimals > 36 {
				t.Fatalf("token %s on %s has impossible decimals %d", symbol, chain, token.Decimals)
			}
			switch network.VMType {
			case "evm":
				if !common.IsHexAddress(token.Address) {
					t.Fatalf("token %s on %s is not an EVM address: %s", symbol, chain, token.Address)
				}
				if token.Address != common.HexToAddress(token.Address).Hex() {
					t.Fatalf("token %s on %s is not EIP-55 checksum: %s", symbol, chain, token.Address)
				}
			case "svm":
				if _, err := base58.Decode(token.Address); err != nil {
					t.Fatalf("token %s on %s is not base58: %v", symbol, chain, err)
				}
			default:
				t.Fatalf("chain %s has unknown vmType %q", chain, network.VMType)
			}
		}
	}
}

func TestRegistry_NetworksShape(t *testing.T) {
	baseDir := findRepoBase(t)
	networks, err := LoadNetworks(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	for slug, network := range networks {
		if strings.HasPrefix(slug, "_") {
			t.Fatalf("unexpected metadata key %q in networks", slug)
		}
		if network.VMType != "evm" && network.VMType != "svm" {
			t.Fatalf("chain %s has invalid vmType %q", slug, network.VMType)
		}
		if network.RelayChainID == 0 {
			t.Fatalf("chain %s missing relayChainId", slug)
		}
		if len(network.SeedEndpoints()) < 3 || len(network.SeedEndpoints()) > 5 {
			t.Fatalf("chain %s must list 3-5 seed RPCs, got %d", slug, len(network.SeedEndpoints()))
		}
		if network.VMType == "evm" && network.ChainID == 0 {
			t.Fatalf("evm chain %s missing chainId", slug)
		}
		if network.VMType == "svm" && network.ChainID != 0 {
			t.Fatalf("solana chain %s must not carry an EVM chainId", slug)
		}
		if network.VMType == "svm" && strings.TrimSpace(network.Cluster) == "" {
			t.Fatalf("solana chain %s missing cluster", slug)
		}
		httpEndpoints := 0
		for _, endpoint := range network.SeedEndpoints() {
			lower := strings.ToLower(endpoint)
			switch {
			case strings.HasPrefix(lower, "https://"):
				httpEndpoints++
			case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "wss://"), strings.HasPrefix(lower, "ws://"):
			default:
				t.Fatalf("chain %s seed RPC has unsupported scheme: %s", slug, endpoint)
			}
		}
		if httpEndpoints == 0 {
			t.Fatalf("chain %s must list at least one http(s) seed RPC", slug)
		}
		if !isHTTPRPCURL(network.PrimaryRPC()) {
			t.Fatalf("chain %s primary RPC must be http(s): %s", slug, network.PrimaryRPC())
		}
		for _, address := range []string{network.Contracts.WrappedNative, network.Contracts.EIP7702Executor} {
			if address == "" {
				continue
			}
			if network.VMType == "evm" && !IsEVMChecksumAddress(address) {
				t.Fatalf("chain %s contract is not checksum: %s", slug, address)
			}
		}
	}
}

func TestRegistry_TokenDecimals(t *testing.T) {
	baseDir := findRepoBase(t)
	tokens, err := LoadTokens(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]map[string]uint8{
		"bsc":      {"USDT": 18, "USDC": 18},
		"ethereum": {"USDT": 6, "USDC": 6},
		"arbitrum": {"USDT": 6, "USDC": 6},
		"base":     {"USDT": 6, "USDC": 6},
		"optimism": {"USDT": 6, "USDC": 6},
		"polygon":  {"USDT": 6, "USDC": 6},
		"solana":   {"USDT": 6, "USDC": 6},
	}
	for chain, symbols := range expected {
		for symbol, decimals := range symbols {
			token, ok := tokens[chain][symbol]
			if !ok {
				t.Fatalf("missing %s on %s", symbol, chain)
			}
			if token.Decimals != decimals {
				t.Fatalf("%s on %s decimals = %d, want %d", symbol, chain, token.Decimals, decimals)
			}
		}
	}
}

func TestRegistry_RelayChainMapping(t *testing.T) {
	baseDir := findRepoBase(t)
	networks, err := LoadNetworks(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	solana, ok := networks["solana"]
	if !ok {
		t.Fatal("solana missing in networks.json")
	}
	if solana.RelayChainID != 792703809 {
		t.Fatalf("solana relayChainId = %d, want 792703809", solana.RelayChainID)
	}
	for slug, network := range networks {
		if network.VMType == "evm" && network.RelayChainID != network.ChainID {
			t.Fatalf("evm chain %s relayChainId %d must equal chainId %d", slug, network.RelayChainID, network.ChainID)
		}
	}
}

func TestLoadTransferSettings_ReadsMetadata(t *testing.T) {
	settings, err := LoadTransferSettings(findRepoBase(t))
	if err != nil {
		t.Fatal(err)
	}
	if settings.EIP7702MinCalls != 3 || settings.EIP7702MinCallsWhenDelegated != 2 {
		t.Fatalf("unexpected thresholds: %+v", settings)
	}
}

func findRepoBase(t *testing.T) string {
	t.Helper()
	candidates := []string{"..", "../..", "../../.."}
	for _, candidate := range candidates {
		networksPath := filepath.Join(candidate, "networks.json")
		tokensPath := filepath.Join(candidate, "tokens.json")
		if _, err := os.Stat(networksPath); err == nil {
			if _, err := os.Stat(tokensPath); err == nil {
				absolute, err := filepath.Abs(candidate)
				if err != nil {
					t.Fatal(err)
				}
				return absolute
			}
		}
	}
	t.Fatal("cannot locate networks.json relative to config test")
	return ""
}
