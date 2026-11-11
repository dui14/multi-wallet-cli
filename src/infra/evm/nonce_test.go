package evm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func nonceServer(t *testing.T, address, nonceHex string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body := make([]byte, request.ContentLength)
		_, _ = request.Body.Read(body)
		_ = request.Body.Close()
		if !strings.Contains(string(body), "eth_getTransactionCount") {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(writer, `{"jsonrpc":"2.0","id":1,"result":"%s"}`, nonceHex)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestPendingNonceParsesHex(t *testing.T) {
	server := nonceServer(t, "0xabc", "0x1a")
	nonce, err := NewClient().PendingNonce(context.Background(), server.URL, "0xabc")
	if err != nil {
		t.Fatal(err)
	}
	if nonce != 26 {
		t.Fatalf("expected nonce 26, got %d", nonce)
	}
}

func TestPendingNonceServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"unauthorized"}}`))
	}))
	defer server.Close()
	if _, err := NewClient().PendingNonce(context.Background(), server.URL, "0xabc"); err == nil {
		t.Fatal("expected error")
	}
}

func TestPendingNonceMasksURLInTransportError(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	rpcURL := closed.URL + "/rpc?apiKey=hidden"
	closed.Close()
	_, err := NewClient().PendingNonce(context.Background(), rpcURL, "0xabc")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "hidden") {
		t.Fatalf("error leaked key: %v", err)
	}
}

func TestCrossPendingNonceAgrees(t *testing.T) {
	nodeA := nonceServer(t, "0xabc", "0x5")
	nodeB := nonceServer(t, "0xabc", "0x5")
	nonce, err := NewClient().CrossPendingNonce(context.Background(), []string{nodeA.URL, nodeB.URL}, "0xabc")
	if err != nil {
		t.Fatal(err)
	}
	if nonce != 5 {
		t.Fatalf("expected 5, got %d", nonce)
	}
}

func TestCrossPendingNonceMismatchNeedsThirdNode(t *testing.T) {
	nodeA := nonceServer(t, "0xabc", "0x5")
	nodeB := nonceServer(t, "0xabc", "0x6")
	client := NewClient()
	if _, err := client.CrossPendingNonce(context.Background(), []string{nodeA.URL, nodeB.URL}, "0xabc"); err == nil {
		t.Fatal("expected mismatch error with two nodes")
	}
	nodeC := nonceServer(t, "0xabc", "0x6")
	nonce, err := client.CrossPendingNonce(context.Background(), []string{nodeA.URL, nodeB.URL, nodeC.URL}, "0xabc")
	if err != nil {
		t.Fatal(err)
	}
	if nonce != 6 {
		t.Fatalf("expected third node majority 6, got %d", nonce)
	}
}

func TestCrossPendingNonceThreeWayMismatchFails(t *testing.T) {
	nodeA := nonceServer(t, "0xabc", "0x5")
	nodeB := nonceServer(t, "0xabc", "0x6")
	nodeC := nonceServer(t, "0xabc", "0x7")
	_, err := NewClient().CrossPendingNonce(context.Background(), []string{nodeA.URL, nodeB.URL, nodeC.URL}, "0xabc")
	if err == nil {
		t.Fatal("expected mismatch error")
	}
}
