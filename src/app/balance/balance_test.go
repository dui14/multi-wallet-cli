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
	"sync/atomic"
	"testing"

	"multi-wallet-cli/domain/wallet"
)

const balanceNetworks = `{
  "_metadata": { "verifiedAt": "2026-10-07" },
  "linea-mainnet": {
    "name": "Linea Mainnet",
    "vmType": "evm",
    "chainId": 59144,
    "relayChainId": 59144,
    "nativeSymbol": "ETH",
    "nativeDecimals": 18,
    "eip1559": true,
    "seedRpcs": ["wss://linea-rpc.publicnode.com", "https://dead.example/rpc", "https://live.example/rpc"],
    "explorer": "",
    "contracts": { "wrappedNative": "" }
  }
}`

const balanceTokens = `{
  "_metadata": { "verifiedAt": "2026-10-07" }
}`

const testNativeBalance = int64(1_000_000)

func newBalanceService(t *testing.T, liveURL string, deadURL string) *Service {
	t.Helper()
	baseDir := t.TempDir()
	networks := strings.ReplaceAll(balanceNetworks, "https://live.example/rpc", liveURL)
	networks = strings.ReplaceAll(networks, "https://dead.example/rpc", deadURL)
	if err := os.WriteFile(filepath.Join(baseDir, "networks.json"), []byte(networks), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "tokens.json"), []byte(balanceTokens), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := New(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func scanRequest() Request {
	return Request{
		Wallets: []wallet.PublicWallet{{Label: "w1", Address: "0xfD9E7639aa9c3A3d4e0F3f2C1b0a9F8E5d525E74", ChainKind: wallet.ChainKindEVM}},
		Chains:  []string{"linea-mainnet"},
		Native:  true,
	}
}

func TestScanRotatesPastWebSocketAndFailingEndpoint(t *testing.T) {
	var deadCalls, liveCalls int64
	live := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		atomic.AddInt64(&liveCalls, 1)
		body, _ := io.ReadAll(request.Body)
		_ = request.Body.Close()
		var requests []struct {
			ID int `json:"id"`
		}
		_ = json.Unmarshal(body, &requests)
		responses := make([]map[string]any, 0, len(requests))
		for _, item := range requests {
			responses = append(responses, map[string]any{"jsonrpc": "2.0", "id": item.ID, "result": "0x" + new(big.Int).SetInt64(testNativeBalance).Text(16)})
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(responses)
	}))
	defer live.Close()
	dead := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&deadCalls, 1)
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer dead.Close()
	service := newBalanceService(t, live.URL, dead.URL)
	report, err := service.Scan(context.Background(), scanRequest())
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt64(&deadCalls) == 0 {
		t.Fatal("expected the failing http endpoint to be attempted before failover")
	}
	if atomic.LoadInt64(&liveCalls) == 0 {
		t.Fatal("expected failover to the live endpoint")
	}
	if len(report.Rows) != 1 {
		t.Fatalf("expected one balance row, got %d", len(report.Rows))
	}
	row := report.Rows[0]
	if row.Err != nil {
		t.Fatalf("expected a successful balance row, got %v", row.Err)
	}
	if row.Raw == nil || row.Raw.Cmp(big.NewInt(testNativeBalance)) != 0 {
		t.Fatalf("unexpected balance: %v", row.Raw)
	}
	if row.Decimals != 18 {
		t.Fatalf("unexpected decimals: %d", row.Decimals)
	}
}

func TestScanReportsRPCErrorWhenEveryEndpointFails(t *testing.T) {
	service := newBalanceService(t, "https://dead.example/rpc", "https://dead.example/rpc")
	report, err := service.Scan(context.Background(), scanRequest())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Rows) != 1 || report.Rows[0].Err == nil {
		t.Fatalf("expected one error row, got %+v", report.Rows)
	}
	if got := report.Rows[0].Token; got != "RPC" {
		t.Fatalf("expected RPC error row, got %q", got)
	}
}
