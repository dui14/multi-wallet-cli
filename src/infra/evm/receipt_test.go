package evm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseReceipt_PendingNull(t *testing.T) {
	receipt, err := parseReceipt(json.RawMessage("null"), "0xabc")
	if err != nil {
		t.Fatal(err)
	}
	if receipt != nil {
		t.Fatal("pending receipt must be nil")
	}
}

func TestParseReceipt_HexGethShape(t *testing.T) {
	raw := json.RawMessage(`{"status":"0x1","blockNumber":"0x10","gasUsed":"0x5208","effectiveGasPrice":"0x3b9aca00"}`)
	receipt, err := parseReceipt(raw, "0xdead")
	if err != nil {
		t.Fatal(err)
	}
	if receipt == nil || receipt.Status != 1 || receipt.BlockNumber.Uint64() != 16 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if receipt.GasUsed.Uint64() != 21000 {
		t.Fatalf("gas used = %s", receipt.GasUsed)
	}
}

func TestParseReceipt_NumericL2Shape(t *testing.T) {
	raw := json.RawMessage(`{"status":1,"blockNumber":12345,"gasUsed":21000,"effectiveGasPrice":1000000000,"l1Fee":"0x1"}`)
	receipt, err := parseReceipt(raw, "0xdead")
	if err != nil {
		t.Fatal(err)
	}
	if receipt == nil || receipt.Status != 1 || receipt.BlockNumber.Uint64() != 12345 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestGetReceipt_PendingThenMined(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body := make([]byte, request.ContentLength)
		_, _ = request.Body.Read(body)
		_ = request.Body.Close()
		if !strings.Contains(string(body), "eth_getTransactionReceipt") {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		calls++
		writer.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
			return
		}
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"status":"0x1","blockNumber":"0x2a","gasUsed":"0x5208"}}`))
	}))
	defer server.Close()
	client := NewClient()
	first, err := client.GetReceipt(context.Background(), server.URL, "0xabc")
	if err != nil {
		t.Fatal(err)
	}
	if first != nil {
		t.Fatal("first poll must be pending")
	}
	second, err := client.GetReceipt(context.Background(), server.URL, "0xabc")
	if err != nil {
		t.Fatal(err)
	}
	if second == nil || second.BlockNumber.Uint64() != 42 {
		t.Fatalf("expected mined receipt, got %+v", second)
	}
}

func TestConfirmTx_MinedWithoutReceipt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body := make([]byte, request.ContentLength)
		_, _ = request.Body.Read(body)
		_ = request.Body.Close()
		writer.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "eth_getTransactionReceipt") {
			_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
			return
		}
		if strings.Contains(string(body), "eth_getTransactionByHash") {
			_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"hash":"0xabc","blockNumber":"0x2a"}}`))
			return
		}
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	receipt, err := NewClient().ConfirmTx(context.Background(), server.URL, "0xABC")
	if err != nil {
		t.Fatal(err)
	}
	if receipt == nil || receipt.Status != 1 || receipt.BlockNumber.Uint64() != 42 {
		t.Fatalf("expected mined confirmation, got %+v", receipt)
	}
}

func TestHasTransaction_NullAndPresent(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body := make([]byte, request.ContentLength)
		_, _ = request.Body.Read(body)
		_ = request.Body.Close()
		calls++
		writer.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
			return
		}
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"hash":"0xabc","blockNumber":null}}`))
	}))
	defer server.Close()
	client := NewClient()
	found, err := client.HasTransaction(context.Background(), server.URL, "0xabc")
	if err != nil || found {
		t.Fatalf("pending must be missing, found=%v err=%v", found, err)
	}
	found, err = client.HasTransaction(context.Background(), server.URL, "0xabc")
	if err != nil || !found {
		t.Fatalf("expected mempool hit, found=%v err=%v", found, err)
	}
}

func TestDecodeBigQuantity_HexAndDecimal(t *testing.T) {
	hexValue, err := decodeBigQuantity(json.RawMessage(`"0x10"`), "n")
	if err != nil {
		t.Fatal(err)
	}
	if hexValue.Uint64() != 16 {
		t.Fatalf("hex = %s", hexValue)
	}
	decValue, err := decodeBigQuantity(json.RawMessage(`42`), "n")
	if err != nil {
		t.Fatal(err)
	}
	if decValue.Uint64() != 42 {
		t.Fatalf("decimal = %s", decValue)
	}
}
