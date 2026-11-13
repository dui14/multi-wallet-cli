package evm

import (
	"context"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"multi-wallet-cli/infra/logcsv"
	"multi-wallet-cli/infra/rpc"
)

func TestRaceDiagnosticLogKeepsOriginalEndpointFailure(t *testing.T) {
	statusSrv := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer statusSrv.Close()
	rpcSrv := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"nonce too low"}}`))
	}))
	defer rpcSrv.Close()

	statusURL := statusSrv.URL + "/v1/secret-path?apiKey=supersecret"
	rpcURL := rpcSrv.URL + "/v1/secret-path?apiKey=supersecret"
	dir := t.TempDir()
	writer, err := logcsv.NewWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	pool := rpc.NewSeedPool("ethereum-sepolia", []string{statusURL, rpcURL})
	pool.SetTrace(func(event rpc.TraceEvent) {
		writer.WriteDiag(event.Fields())
	})
	destination := common.HexToAddress("0x0000000000000000000000000000000000000001")
	signed := types.NewTx(&types.LegacyTx{
		Nonce:    2,
		GasPrice: big.NewInt(1),
		Gas:      21000,
		To:       &destination,
		Value:    big.NewInt(0),
	})
	client := NewClient()
	raceErr := pool.Race(rpc.WithTxSeq(context.Background(), 2), func(ctx context.Context, rawURL string) error {
		_, broadcastErr := client.BroadcastTx(ctx, rawURL, signed)
		return broadcastErr
	})
	if raceErr == nil {
		t.Fatal("expected race failure")
	}
	if strings.Contains(raceErr.Error(), "nonce too low") || strings.Contains(raceErr.Error(), "-32000") || strings.Contains(raceErr.Error(), "supersecret") {
		t.Fatalf("user-visible race error changed: %v", raceErr)
	}
	if !strings.Contains(raceErr.Error(), "all endpoints failed") {
		t.Fatalf("user-visible race error: %v", raceErr)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, logcsv.DiagFileName))
	if err != nil {
		t.Fatal(err)
	}
	logText := string(data)
	if !strings.Contains(logText, "nonce too low") {
		t.Fatalf("diagnostic log missing json-rpc message: %s", logText)
	}
	if !strings.Contains(logText, "-32000") {
		t.Fatalf("diagnostic log missing json-rpc code: %s", logText)
	}
	if !strings.Contains(logText, "HTTP 503") {
		t.Fatalf("diagnostic log missing http status: %s", logText)
	}
	if !strings.Contains(logText, "tx 2 before Race got a success") {
		t.Fatalf("diagnostic log missing tx failure source: %s", logText)
	}
	if strings.Contains(logText, "supersecret") || strings.Contains(logText, "secret-path") {
		t.Fatalf("diagnostic log leaked rpc secret: %s", logText)
	}
	if strings.TrimSpace(logText) == "" || !strings.Contains(logText, "race_result") {
		t.Fatalf("diagnostic log missing per-endpoint result: %s", logText)
	}
}
