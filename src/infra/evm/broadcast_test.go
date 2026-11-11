package evm

import (
	"context"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestBroadcastTx_AlreadyKnownReturnsLocalHash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"already known"}}`))
	}))
	defer server.Close()
	signed := mustSignNative(t)
	hash, err := NewClient().BroadcastTx(context.Background(), server.URL, signed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(hash, signed.Hash().Hex()) {
		t.Fatalf("expected local hash %s, got %s", signed.Hash().Hex(), hash)
	}
}

func TestSignTx_EIP1559FromLegacyGasPrice(t *testing.T) {
	signed := mustSignNative(t)
	if signed.Type() != 2 {
		t.Fatalf("expected dynamic fee tx, got type %d", signed.Type())
	}
}

func mustSignNative(t *testing.T) *types.Transaction {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signed, err := SignTx(crypto.FromECDSA(key), 8453, 0, TransferTx{
		To:       "0x0000000000000000000000000000000000000001",
		Value:    big.NewInt(1),
		GasLimit: 21000,
	}, &FeeEstimate{GasPrice: big.NewInt(1000)}, true)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}
