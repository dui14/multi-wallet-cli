package transfer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"multi-wallet-cli/infra/evm"
	"multi-wallet-cli/infra/rpc"
)

func TestWaitReceipt_RotatesPastPendingEndpoint(t *testing.T) {
	pending := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
	}))
	t.Cleanup(pending.Close)
	mined := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		_ = request.Body.Close()
		if !strings.Contains(string(body), "eth_getTransactionReceipt") {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(writer, `{"jsonrpc":"2.0","id":1,"result":{"status":"0x1","blockNumber":"0x64","gasUsed":"0x5208"}}`)
	}))
	t.Cleanup(mined.Close)

	prevTimeout := receiptTimeout
	prevInterval := receiptPollInterval
	prevCall := receiptCallTimeout
	receiptTimeout = 2 * time.Second
	receiptPollInterval = 10 * time.Millisecond
	receiptCallTimeout = time.Second
	t.Cleanup(func() {
		receiptTimeout = prevTimeout
		receiptPollInterval = prevInterval
		receiptCallTimeout = prevCall
	})

	flow := &Flow{
		evm:  evm.NewClient(),
		pool: rpc.NewSeedPool("base", []string{pending.URL, mined.URL}),
		rpc:  pending.URL,
	}
	receipt, err := flow.waitReceipt(context.Background(), "0xabc")
	if err != nil {
		t.Fatal(err)
	}
	if receipt == nil || receipt.BlockNumber.Uint64() != 100 {
		t.Fatalf("expected mined receipt from second RPC, got %+v", receipt)
	}
}

func TestWaitSeen_FindsTxOnSecondRPC(t *testing.T) {
	missing := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
	}))
	t.Cleanup(missing.Close)
	present := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		_ = request.Body.Close()
		if !strings.Contains(string(body), "eth_getTransactionByHash") {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"hash":"0xabc"}}`))
	}))
	t.Cleanup(present.Close)
	prevInterval := receiptPollInterval
	prevCall := receiptCallTimeout
	receiptPollInterval = 10 * time.Millisecond
	receiptCallTimeout = time.Second
	t.Cleanup(func() {
		receiptPollInterval = prevInterval
		receiptCallTimeout = prevCall
	})
	flow := &Flow{
		evm:  evm.NewClient(),
		pool: rpc.NewSeedPool("base", []string{missing.URL, present.URL}),
		rpc:  missing.URL,
	}
	if !flow.waitSeen(context.Background(), "0xabc", time.Second) {
		t.Fatal("expected second RPC to observe the hash")
	}
}

func TestWaitReceipt_ConfirmsMinedTxWithoutReceipt(t *testing.T) {
	lagging := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
	}))
	t.Cleanup(lagging.Close)
	included := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		_ = request.Body.Close()
		writer.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "eth_getTransactionReceipt") {
			_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
			return
		}
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"hash":"0xabc","blockNumber":"0x10"}}`))
	}))
	t.Cleanup(included.Close)
	prevTimeout := receiptTimeout
	prevInterval := receiptPollInterval
	prevCall := receiptCallTimeout
	receiptTimeout = time.Second
	receiptPollInterval = 10 * time.Millisecond
	receiptCallTimeout = time.Second
	t.Cleanup(func() {
		receiptTimeout = prevTimeout
		receiptPollInterval = prevInterval
		receiptCallTimeout = prevCall
	})
	flow := &Flow{
		evm:  evm.NewClient(),
		pool: rpc.NewSeedPool("polygon", []string{lagging.URL, included.URL}),
		rpc:  lagging.URL,
	}
	receipt, err := flow.waitReceipt(context.Background(), "0xabc")
	if err != nil {
		t.Fatal(err)
	}
	if receipt == nil || receipt.BlockNumber.Uint64() != 16 {
		t.Fatalf("expected inclusion from getTransactionByHash, got %+v", receipt)
	}
}
