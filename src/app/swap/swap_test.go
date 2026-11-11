package swap

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

func swapTestService(t *testing.T, handler http.HandlerFunc) *Service {
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := relay.NewClient(".")
	client.SetBaseURL(server.URL)
	engine := relayexec.NewEngine(client, nil, nil, nil)
	networks := map[string]config.Network{
		"base": {Name: "Base", VMType: "evm", ChainID: 8453, RelayChainID: 8453, NativeSymbol: "ETH", NativeDecimals: 18, Explorer: "https://basescan.org/"},
	}
	return NewService(networks, nil, engine, nil)
}

func TestSwapQuoteParsesRoute(t *testing.T) {
	service := swapTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"requestId": "0xreq",
			"steps":     []map[string]any{{"id": "swap", "kind": "transaction", "requestId": "0xreq", "items": []map[string]any{{"status": "incomplete", "data": map[string]any{"to": "0xrouter", "data": "0xswap", "value": "0", "chainId": 8453}}}}},
			"fees":      map[string]any{"gas": "300000", "relayer": "50"},
			"details":   map[string]any{"currency": map[string]any{"symbol": "USDC", "decimals": 6}, "amount": "2500000"},
		})
	})
	quote, err := service.Quote(context.Background(), Request{
		Chain:     "base",
		From:      wallet.PublicWallet{Label: "w1", Address: "0xuser"},
		FromToken: config.Token{Symbol: "ETH"},
		ToToken:   config.Token{Symbol: "USDC", Address: "0xusdc", Decimals: 6},
		Amount:    big.NewInt(1000000000000000000),
	})
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if quote.ExpectedReceive != "2500000" || quote.ReceiveSymbol != "USDC" {
		t.Fatalf("unexpected quote: %+v", quote)
	}
	if quote.Steps != 1 {
		t.Fatalf("expected 1 step, got %d", quote.Steps)
	}
}

func TestSwapQuoteRejectsSameToken(t *testing.T) {
	service := swapTestService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("relay should not be called for an invalid swap")
	})
	if _, err := service.Quote(context.Background(), Request{
		Chain:     "base",
		FromToken: config.Token{Symbol: "USDC", Address: "0xusdc"},
		ToToken:   config.Token{Symbol: "USDC", Address: "0xusdc"},
		Amount:    big.NewInt(1),
	}); err == nil {
		t.Fatal("expected error when from and to tokens match")
	}
}

func TestSwapDoSkipsWhenChainUnknown(t *testing.T) {
	service := swapTestService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("relay should not be called for an unknown chain")
	})
	result := service.Do(context.Background(), Request{
		Chain:  "missing",
		From:   wallet.PublicWallet{Label: "w1"},
		Amount: big.NewInt(1),
	})
	if result.FinalStatus != "skipped" {
		t.Fatalf("expected skipped status, got %s", result.FinalStatus)
	}
}
