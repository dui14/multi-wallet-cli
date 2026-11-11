package balance

import (
	"context"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const customTokens = `{
  "_metadata": { "verifiedAt": "2026-10-07" },
  "linea-mainnet": {
    "USDC": {
      "address": "0x00000000000000000000000000000000000000a1",
      "decimals": 6,
      "symbol": "USDC"
    }
  }
}`

func newCustomService(t *testing.T, tokens string, liveURL string, deadURL string) *Service {
	t.Helper()
	baseDir := t.TempDir()
	networks := strings.ReplaceAll(balanceNetworks, "https://live.example/rpc", liveURL)
	networks = strings.ReplaceAll(networks, "https://dead.example/rpc", deadURL)
	if err := os.WriteFile(filepath.Join(baseDir, "networks.json"), []byte(networks), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "tokens.json"), []byte(tokens), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := New(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// metadataServer answers balanceOf and decimals for the configured token
// addresses so the scan can resolve each token without a real node.
func metadataServer(t *testing.T, balances map[string]*big.Int, decimals map[string]uint64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		_ = request.Body.Close()
		var calls []struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params []struct {
				To   string `json:"to"`
				Data string `json:"data"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &calls)
		responses := make([]map[string]any, 0, len(calls))
		for _, call := range calls {
			result := "0x0"
			if call.Method == "eth_call" && len(call.Params) > 0 {
				address := strings.ToLower(call.Params[0].To)
				switch {
				case strings.HasPrefix(call.Params[0].Data, "0x70a08231"):
					if value, ok := balances[address]; ok {
						result = "0x" + value.Text(16)
					}
				case strings.HasPrefix(call.Params[0].Data, "0x313ce567"):
					if value, ok := decimals[address]; ok {
						result = "0x" + new(big.Int).SetUint64(value).Text(16)
					}
				}
			} else if call.Method == "eth_getBalance" {
				result = "0x0"
			}
			responses = append(responses, map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": result})
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(responses)
	}))
}

func TestScanDeduplicatesCustomAgainstKnownAddress(t *testing.T) {
	address := "0x00000000000000000000000000000000000000a1"
	balance := big.NewInt(1_000_000)
	server := metadataServer(t, map[string]*big.Int{address: balance}, map[string]uint64{address: 6})
	defer server.Close()
	service := newCustomService(t, customTokens, server.URL, server.URL)
	request := scanRequest()
	request.Native = false
	request.Tokens = map[string][]string{"linea-mainnet": {"USDC"}}
	request.Custom = map[string]CustomToken{"linea-mainnet": {Symbol: "SAHARA", Address: address, Decimals: 18}}
	report, err := service.Scan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("expected one deduplicated row, got %d", len(report.Rows))
	}
	row := report.Rows[0]
	if row.Token != "USDC" {
		t.Fatalf("expected the known symbol to win the dedupe, got %q", row.Token)
	}
	if row.Raw == nil || row.Raw.Cmp(balance) != 0 {
		t.Fatalf("unexpected raw balance: %v", row.Raw)
	}
	if row.Decimals != 6 {
		t.Fatalf("expected decimals read from the chain, got %d", row.Decimals)
	}
}

func TestScanUsesCustomMetadataForUnknownAddress(t *testing.T) {
	address := "0x00000000000000000000000000000000000000a2"
	balance := big.NewInt(500_000)
	server := metadataServer(t, map[string]*big.Int{address: balance}, map[string]uint64{address: 18})
	defer server.Close()
	service := newCustomService(t, balanceTokens, server.URL, server.URL)
	request := scanRequest()
	request.Native = false
	request.Custom = map[string]CustomToken{"linea-mainnet": {Symbol: "SAHARA", Address: address, Decimals: 18}}
	report, err := service.Scan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("expected one custom row, got %d", len(report.Rows))
	}
	row := report.Rows[0]
	if row.Token != "SAHARA" {
		t.Fatalf("expected the resolved custom symbol, got %q", row.Token)
	}
	if row.Raw == nil || row.Raw.Cmp(balance) != 0 {
		t.Fatalf("unexpected raw balance: %v", row.Raw)
	}
	if row.Decimals != 18 {
		t.Fatalf("expected 18 decimals, got %d", row.Decimals)
	}
}
