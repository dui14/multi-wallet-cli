package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testClient(handler http.HandlerFunc) *Client {
	server := httptest.NewServer(handler)
	t := &testing.T{}
	t.Cleanup(server.Close)
	client := NewClient(".")
	client.baseURL = server.URL
	return client
}

func TestQuoteParsesStepsAndFees(t *testing.T) {
	client := testClient(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != quotePath {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected bearer auth header")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"requestId": "0xreq",
			"steps": []map[string]any{
				{
					"id": "approve", "kind": "transaction", "requestId": "0xreq",
					"items": []map[string]any{{"status": "incomplete", "data": map[string]any{"to": "0xtoken", "data": "0xapprove", "value": "0", "chainId": 8453, "gas": "50000"}}},
				},
				{
					"id": "swap", "kind": "transaction", "requestId": "0xreq",
					"items": []map[string]any{{"status": "incomplete", "data": map[string]any{"to": "0xrouter", "data": "0xswap", "value": "1000", "chainId": 8453, "maxFeePerGas": "6500000"}}},
				},
			},
			"fees":    map[string]any{"gas": "2000000000", "relayer": "100"},
			"details": map[string]any{"currency": map[string]any{"symbol": "USDC", "decimals": 6}, "amount": "999000"},
		})
	})
	client.apiKey = "test-key"

	quote, err := client.Quote(context.Background(), QuoteRequest{
		User: "0xuser", OriginChainID: 8453, DestinationChainID: 8453,
		OriginCurrency: NativeSentinel, DestinationCurrency: "0xusdc", Amount: "1000000000000000", TradeType: "EXACT_INPUT",
	})
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if len(quote.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(quote.Steps))
	}
	if quote.Steps[0].Items[0].Data.To != "0xtoken" {
		t.Fatalf("approve step to mismatch: %q", quote.Steps[0].Items[0].Data.To)
	}
	if quote.Steps[1].Items[0].Data.Value != "1000" {
		t.Fatalf("swap value mismatch: %q", quote.Steps[1].Items[0].Data.Value)
	}
	if quote.Fees.Gas != "2000000000" {
		t.Fatalf("fee gas mismatch: %q", quote.Fees.Gas)
	}
	if quote.Details.Currency.Symbol != "USDC" {
		t.Fatalf("details symbol mismatch: %q", quote.Details.Currency.Symbol)
	}
}

func TestQuoteRejectsEmptySteps(t *testing.T) {
	client := testClient(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"requestId":"0x","steps":[]}`))
	})
	if _, err := client.Quote(context.Background(), QuoteRequest{TradeType: "EXACT_INPUT"}); err == nil {
		t.Fatal("expected error when relay returns no steps")
	}
}

func TestChainsParsesVMType(t *testing.T) {
	client := testClient(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 8453, "name": "Base", "vmType": "evm"},
			{"id": 792703809, "name": "Solana", "vmType": "svm"},
		})
	})
	chains, err := client.Chains(context.Background())
	if err != nil {
		t.Fatalf("chains: %v", err)
	}
	if len(chains) != 2 || chains[1].VMType != "svm" {
		t.Fatalf("unexpected chains: %+v", chains)
	}
}

func TestStatusParsesLifecycle(t *testing.T) {
	client := testClient(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("requestId") != "0xreq" {
			t.Errorf("missing requestId query")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","inTxHashes":["0xin"],"txHashes":["0xout"],"originChainId":8453,"destinationChainId":56}`))
	})
	status, err := client.Status(context.Background(), "0xreq")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Status != StatusSuccess {
		t.Fatalf("status mismatch: %s", status.Status)
	}
	if len(status.TxHashes) != 1 || status.TxHashes[0] != "0xout" {
		t.Fatalf("tx hashes mismatch: %+v", status.TxHashes)
	}
}

func TestQuoteRateLimitReturnsError(t *testing.T) {
	client := testClient(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	if _, err := client.Quote(context.Background(), QuoteRequest{TradeType: "EXACT_INPUT"}); err == nil {
		t.Fatal("expected rate limit error")
	}
}
