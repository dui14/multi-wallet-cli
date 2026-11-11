package transfer

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"multi-wallet-cli/infra/config"
	"multi-wallet-cli/infra/evm"
	"multi-wallet-cli/infra/rpc"
)

func TestCollectFees_TakesHigherAcrossRPCs(t *testing.T) {
	low := feeServer(t, "0x64", "0xc8", "0x1")
	high := feeServer(t, "0xc8", "0x190", "0x2")
	flow := &Flow{
		evm:  evm.NewClient(),
		pool: rpc.NewSeedPool("base", []string{low.URL, high.URL}),
		rpc:  low.URL,
	}
	prev := receiptCallTimeout
	receiptCallTimeout = time.Second
	t.Cleanup(func() { receiptCallTimeout = prev })
	estimate, err := flow.collectFees(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if estimate.MaxFeePerGas.Uint64() < 802 {
		t.Fatalf("expected merged maxFee from higher node, got %s", estimate.MaxFeePerGas)
	}
}

func TestClampNativeMax_ReducesValueForFeeCap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x186a0"}`))
	}))
	t.Cleanup(server.Close)
	flow := &Flow{evm: evm.NewClient(), rpc: server.URL, network: config.Network{EIP1559: true}}
	prev := receiptCallTimeout
	receiptCallTimeout = time.Second
	t.Cleanup(func() { receiptCallTimeout = prev })
	fees := &evm.FeeEstimate{MaxFeePerGas: big.NewInt(2), MaxPriorityFeePerGas: big.NewInt(1), GasPrice: big.NewInt(2)}
	got, err := flow.clampNativeMax(context.Background(), "0x1", big.NewInt(90000), 21000, fees)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cmp(big.NewInt(58000)) != 0 {
		t.Fatalf("expected 58000 leftover after gas, got %s", got)
	}
}

func feeServer(t *testing.T, base, next, tip string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body := make([]byte, request.ContentLength)
		_, _ = request.Body.Read(body)
		_ = request.Body.Close()
		writer.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "eth_feeHistory") {
			_, _ = fmt.Fprintf(writer, `{"jsonrpc":"2.0","id":1,"result":{"baseFeePerGas":["%s","%s"],"reward":[["%s"]]}}`, base, next, tip)
			return
		}
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"no"}}`))
	}))
	t.Cleanup(server.Close)
	return server
}
