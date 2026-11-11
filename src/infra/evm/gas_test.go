package evm

import (
	"context"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGasPriceParsesHex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body := make([]byte, request.ContentLength)
		_, _ = request.Body.Read(body)
		_ = request.Body.Close()
		if !strings.Contains(string(body), "eth_gasPrice") {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x3b9aca00"}`))
	}))
	defer server.Close()
	price, err := NewClient().GasPrice(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if price.String() != "1000000000" {
		t.Fatalf("expected 1 gwei, got %s", price)
	}
}

func TestEstimateFeesUsesFeeHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body := make([]byte, request.ContentLength)
		_, _ = request.Body.Read(body)
		_ = request.Body.Close()
		writer.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "eth_feeHistory") {
			_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"baseFeePerGas":["0x64","0xc8"],"reward":[["0x1"]],"oldestBlock":"0x1"}}`))
			return
		}
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x64"}`))
	}))
	defer server.Close()
	estimate, err := NewClient().EstimateFees(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if estimate.MaxPriorityFeePerGas.String() != "1" {
		t.Fatalf("expected tip 1, got %s", estimate.MaxPriorityFeePerGas)
	}
	if estimate.MaxFeePerGas.String() != "601" {
		t.Fatalf("expected maxFee 3*200+1=601, got %s", estimate.MaxFeePerGas)
	}
	if estimate.GasPrice.String() != "201" {
		t.Fatalf("expected gasPrice 201, got %s", estimate.GasPrice)
	}
}

func TestEstimateFeesFallsBackToGasPrice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body := make([]byte, request.ContentLength)
		_, _ = request.Body.Read(body)
		_ = request.Body.Close()
		writer.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "eth_feeHistory") {
			_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found"}}`))
			return
		}
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x64"}`))
	}))
	defer server.Close()
	estimate, err := NewClient().EstimateFees(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if estimate.GasPrice.String() != "100" {
		t.Fatalf("expected gas price 100, got %s", estimate.GasPrice)
	}
	if estimate.MaxPriorityFeePerGas.String() != "100" {
		t.Fatalf("expected tip filled from gas price, got %s", estimate.MaxPriorityFeePerGas)
	}
	if estimate.MaxFeePerGas.String() != "300" {
		t.Fatalf("expected maxFee 3*gasPrice, got %s", estimate.MaxFeePerGas)
	}
}

func TestEstimateFeesParsesNumericHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body := make([]byte, request.ContentLength)
		_, _ = request.Body.Read(body)
		_ = request.Body.Close()
		writer.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "eth_feeHistory") {
			_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"baseFeePerGas":[100,200],"reward":[[3]]}}`))
			return
		}
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	estimate, err := NewClient().EstimateFees(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if estimate.MaxFeePerGas.String() != "603" {
		t.Fatalf("expected maxFee 3*200+3=603, got %s", estimate.MaxFeePerGas)
	}
}

func TestMergeFeeEstimatesTakesHigher(t *testing.T) {
	merged := MergeFeeEstimates([]*FeeEstimate{
		{GasPrice: bigInt(10), MaxFeePerGas: bigInt(20), MaxPriorityFeePerGas: bigInt(1)},
		{GasPrice: bigInt(12), MaxFeePerGas: bigInt(40), MaxPriorityFeePerGas: bigInt(2)},
	})
	if merged.MaxFeePerGas.String() != "40" || merged.MaxPriorityFeePerGas.String() != "2" {
		t.Fatalf("unexpected merge: %+v", merged)
	}
}

func bigInt(v int64) *big.Int {
	return new(big.Int).SetInt64(v)
}

func TestCrossGasPriceMismatchResolvesWithThirdNode(t *testing.T) {
	priceServer := func(value string) *httptest.Server {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"` + value + `"}`))
		}))
		t.Cleanup(server.Close)
		return server
	}
	nodeA := priceServer("0x64")
	nodeB := priceServer("0x65")
	nodeC := priceServer("0x65")
	price, err := NewClient().CrossGasPrice(context.Background(), []string{nodeA.URL, nodeB.URL, nodeC.URL})
	if err != nil {
		t.Fatal(err)
	}
	if price.String() != "101" {
		t.Fatalf("expected majority 101, got %s", price)
	}
}
