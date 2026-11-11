package bridge

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"multi-wallet-cli/app/relayexec"
	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/aggregator/relay"
	"multi-wallet-cli/infra/config"
)

func bridgeTestService(t *testing.T, handler http.HandlerFunc) *Service {
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := relay.NewClient(".")
	client.SetBaseURL(server.URL)
	engine := relayexec.NewEngine(client, nil, nil, nil)
	networks := map[string]config.Network{
		"base":     {Name: "Base", VMType: "evm", ChainID: 8453, RelayChainID: 8453, NativeSymbol: "ETH", NativeDecimals: 18, Explorer: "https://basescan.org/"},
		"arbitrum": {Name: "Arbitrum", VMType: "evm", ChainID: 42161, RelayChainID: 42161, NativeSymbol: "ETH", NativeDecimals: 18, Explorer: "https://arbiscan.io/"},
	}
	return NewService(networks, nil, engine, nil)
}

func TestBridgeQuoteParsesRoute(t *testing.T) {
	service := bridgeTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"requestId": "0xreq",
			"steps":     []map[string]any{{"id": "bridge", "kind": "transaction", "requestId": "0xreq", "items": []map[string]any{{"status": "incomplete", "data": map[string]any{"to": "0xrelay", "data": "0xbridge", "value": "0", "chainId": 8453}}}}},
			"fees":      map[string]any{"gas": "400000", "relayer": "120"},
			"details":   map[string]any{"currency": map[string]any{"symbol": "USDC", "decimals": 6}, "amount": "99000000"},
		})
	})
	quote, err := service.Quote(context.Background(), Request{
		FromChain: "base",
		ToChain:   "arbitrum",
		From:      wallet.PublicWallet{Label: "w1", Address: "0xuser"},
		FromToken: config.Token{Symbol: "USDC", Address: "0xusdcA", Decimals: 6},
		ToToken:   config.Token{Symbol: "USDC", Address: "0xusdcB", Decimals: 6},
		Amount:    big.NewInt(100000000),
	})
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if quote.ExpectedReceive != "99000000" || quote.ReceiveSymbol != "USDC" {
		t.Fatalf("unexpected quote: %+v", quote)
	}
	if !quote.CrossAsset {
		t.Fatal("expected cross-asset flag for differing token addresses")
	}
	if quote.RelayerFee != "120" {
		t.Fatalf("unexpected relayer fee: %s", quote.RelayerFee)
	}
}

func TestBridgeQuoteSameAssetNotCrossAsset(t *testing.T) {
	service := bridgeTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"requestId": "0xreq",
			"steps":     []map[string]any{{"id": "bridge", "kind": "transaction", "requestId": "0xreq", "items": []map[string]any{{"status": "incomplete", "data": map[string]any{"to": "0xrelay", "data": "0xbridge", "value": "0", "chainId": 8453}}}}},
			"fees":      map[string]any{"gas": "1", "relayer": "1"},
			"details":   map[string]any{"currency": map[string]any{"symbol": "USDC", "decimals": 6}, "amount": "100"},
		})
	})
	quote, err := service.Quote(context.Background(), Request{
		FromChain: "base",
		ToChain:   "arbitrum",
		FromToken: config.Token{Symbol: "USDC", Address: "0xusdc"},
		ToToken:   config.Token{Symbol: "USDC", Address: "0xusdc"},
		Amount:    big.NewInt(100),
	})
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if quote.CrossAsset {
		t.Fatal("same address on both sides should not be cross-asset")
	}
}

func TestBridgeQuoteRejectsSameChain(t *testing.T) {
	service := bridgeTestService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("relay should not be called for a same-chain bridge")
	})
	if _, err := service.Quote(context.Background(), Request{
		FromChain: "base",
		ToChain:   "base",
		Amount:    big.NewInt(1),
	}); err == nil {
		t.Fatal("expected error when source and destination chains match")
	}
}

func TestBridgeQuoteRejectsUnknownDestination(t *testing.T) {
	service := bridgeTestService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("relay should not be called for an unknown chain")
	})
	if _, err := service.Quote(context.Background(), Request{
		FromChain: "base",
		ToChain:   "missing",
		Amount:    big.NewInt(1),
	}); err == nil {
		t.Fatal("expected error for unknown destination chain")
	}
}

func TestBridgeDoSkipsOnValidationFailure(t *testing.T) {
	service := bridgeTestService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("relay should not be called when validation fails")
	})
	result := service.Do(context.Background(), Request{
		FromChain: "base",
		ToChain:   "base",
		From:      wallet.PublicWallet{Label: "w1"},
		Amount:    big.NewInt(1),
	})
	if result.FinalStatus != "skipped" {
		t.Fatalf("expected skipped status, got %s", result.FinalStatus)
	}
	if result.ErrorCode == "" {
		t.Fatal("expected an error code on the skipped result")
	}
}
