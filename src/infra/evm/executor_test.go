package evm

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestPackExecute_RoundTrip(t *testing.T) {
	target := common.HexToAddress("0x1111111111111111111111111111111111111111")
	packed, err := PackExecute([]Call{{To: target, Value: big.NewInt(5), Data: []byte{0x01, 0x02}}})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := abi.JSON(strings.NewReader(executeABIJSON))
	if err != nil {
		t.Fatal(err)
	}
	method, err := parsed.MethodById(packed[:4])
	if err != nil {
		t.Fatal(err)
	}
	values, err := method.Inputs.Unpack(packed[4:])
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 {
		t.Fatalf("expected one argument, got %d", len(values))
	}
}

func TestAuthorizationForOwner_RejectsZeroChainID(t *testing.T) {
	_, err := AuthorizationForOwner(0, "0x1111111111111111111111111111111111111111", 1)
	if err == nil {
		t.Fatal("expected zero chain id to be rejected")
	}
}

func TestSignSetCodeTx_AuthNonceIsTxNoncePlusOne(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	owner := crypto.PubkeyToAddress(key.PublicKey)
	auth, err := AuthorizationForOwner(11155111, "0x2222222222222222222222222222222222222222", 7)
	if err != nil {
		t.Fatal(err)
	}
	fees := &FeeEstimate{MaxFeePerGas: big.NewInt(3), MaxPriorityFeePerGas: big.NewInt(1), GasPrice: big.NewInt(3)}
	signed, err := SignSetCodeTx(crypto.FromECDSA(key), 11155111, 7, TransferTx{
		From:     owner.Hex(),
		To:       owner.Hex(),
		Value:    big.NewInt(0),
		GasLimit: 120000,
		Data:     []byte{0xaa},
	}, fees, auth)
	if err != nil {
		t.Fatal(err)
	}
	if signed.Type() != types.SetCodeTxType {
		t.Fatalf("expected type 4, got %d", signed.Type())
	}
	if signed.Nonce() != 7 {
		t.Fatalf("expected tx nonce 7, got %d", signed.Nonce())
	}
	list := signed.SetCodeAuthorizations()
	if len(list) != 1 {
		t.Fatalf("expected one authorization, got %d", len(list))
	}
	if list[0].Nonce != 8 {
		t.Fatalf("expected authorization nonce 8, got %d", list[0].Nonce)
	}
	if list[0].ChainID.IsZero() {
		t.Fatal("authorization chain id must not be zero")
	}
	authority, err := list[0].Authority()
	if err != nil {
		t.Fatal(err)
	}
	if authority != owner {
		t.Fatalf("authority %s is not the owner", authority.Hex())
	}
}

func TestSignSetCodeTx_RejectsMismatchedAuthNonce(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	auth, err := AuthorizationForOwner(1, "0x2222222222222222222222222222222222222222", 4)
	if err != nil {
		t.Fatal(err)
	}
	fees := &FeeEstimate{MaxFeePerGas: big.NewInt(3), MaxPriorityFeePerGas: big.NewInt(1)}
	_, err = SignSetCodeTx(crypto.FromECDSA(key), 1, 9, TransferTx{To: crypto.PubkeyToAddress(key.PublicKey).Hex(), GasLimit: 21000}, fees, auth)
	if err == nil {
		t.Fatal("expected mismatched authorization nonce to be rejected")
	}
}

func TestDelegatedTo_RuntimeHashOrDesignator(t *testing.T) {
	executor := common.HexToAddress("0x2222222222222222222222222222222222222222")
	runtime := []byte{0x60, 0x00, 0x56}
	hash := crypto.Keccak256Hash(runtime)
	if !DelegatedTo(runtime, executor, hash) {
		t.Fatal("expected runtime code to count as delegated")
	}
	if !DelegatedTo(types.AddressToDelegation(executor), executor, hash) {
		t.Fatal("expected the delegation designator to count as delegated")
	}
	other := common.HexToAddress("0x3333333333333333333333333333333333333333")
	if DelegatedTo(types.AddressToDelegation(other), executor, hash) {
		t.Fatal("a foreign delegation must not count")
	}
	if DelegatedTo([]byte{0x01, 0x02}, executor, hash) {
		t.Fatal("foreign code must not count as delegated")
	}
}

func TestExecutorCodeMatches_Mismatch(t *testing.T) {
	code := []byte{0x60, 0x00}
	if err := ExecutorCodeMatches(code, crypto.Keccak256Hash([]byte{0xff}).Hex()); !errors.Is(err, ErrExecutorCodeMismatch) {
		t.Fatalf("expected mismatch, got %v", err)
	}
	if err := ExecutorCodeMatches(code, crypto.Keccak256Hash(code).Hex()); err != nil {
		t.Fatal(err)
	}
}

func TestSupportsSetCodeTx_UsesRequestsHash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body := make([]byte, request.ContentLength)
		_, _ = request.Body.Read(body)
		writer.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "eth_getBlockByNumber") && strings.Contains(string(body), "prague") {
			_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"gasLimit":"0x1","requestsHash":"0xabc"}}`))
			return
		}
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"gasLimit":"0x1"}}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient()
	supported, err := client.SupportsSetCodeTx(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if supported {
		t.Fatal("expected a block without requestsHash to be unsupported")
	}
	prague := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"gasLimit":"0x1","requestsHash":"0xabc"}}`))
	}))
	t.Cleanup(prague.Close)
	supported, err = client.SupportsSetCodeTx(context.Background(), prague.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !supported {
		t.Fatal("expected requestsHash to enable set-code transactions")
	}
}

func TestEstimateGasLimit_IncludesAuthorizationList(t *testing.T) {
	var sawAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		params, _ := payload["params"].([]any)
		if len(params) > 0 {
			raw, _ := json.Marshal(params[0])
			sawAuth = strings.Contains(string(raw), "authorizationList")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x5208"}`))
	}))
	t.Cleanup(server.Close)
	auth, err := AuthorizationForOwner(1, "0x2222222222222222222222222222222222222222", 3)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewClient().EstimateGasLimit(context.Background(), server.URL, TransferTx{
		From: "0x1111111111111111111111111111111111111111",
		To:   "0x1111111111111111111111111111111111111111",
		Auth: &auth,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawAuth {
		t.Fatal("expected authorizationList on the estimate")
	}
}

