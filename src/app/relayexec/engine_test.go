package relayexec

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"multi-wallet-cli/infra/aggregator/relay"
	"multi-wallet-cli/infra/logcsv"
)

func TestNormalizeError_MapsCodes(t *testing.T) {
	cases := map[string]string{
		"relay rate limited":        logcsv.ErrRPCTimeout,
		"relay returned no route":   logcsv.ErrNoRoute,
		"insufficient balance":      logcsv.ErrInsufficientFunds,
		"quote expired":             logcsv.ErrQuoteExpired,
		"context deadline exceeded": logcsv.ErrRPCTimeout,
		"totally unexpected":        logcsv.ErrUnknown,
	}
	for message, expected := range cases {
		if got := NormalizeError(errors.New(message)); got != expected {
			t.Fatalf("message %q expected %s got %s", message, expected, got)
		}
	}
	if NormalizeError(nil) != "" {
		t.Fatal("nil error should map to empty code")
	}
}

func TestParseQuantity(t *testing.T) {
	if got := ParseQuantity("1000000000000000000").String(); got != "1000000000000000000" {
		t.Fatalf("decimal parse failed: %s", got)
	}
	if got := ParseQuantity("0x10").String(); got != "16" {
		t.Fatalf("hex parse failed: %s", got)
	}
	if ParseQuantity("").Sign() != 0 {
		t.Fatal("empty should be zero")
	}
}

func TestDecodeHex(t *testing.T) {
	decoded := DecodeHex("0xabcdef")
	if len(decoded) != 3 || decoded[0] != 0xab {
		t.Fatalf("hex decode failed: %v", decoded)
	}
	if DecodeHex("") != nil {
		t.Fatal("empty data should be nil")
	}
}

func TestPollStatus_MapsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","txHashes":["0xout"]}`))
	}))
	defer server.Close()
	client := relay.NewClient(".")
	client.SetBaseURL(server.URL)
	engine := &Engine{relay: client}

	status, code := engine.PollStatus(context.Background(), "0xreq")
	if status != logcsv.StatusBridgeCompleted || code != "" {
		t.Fatalf("expected bridge_completed, got %s/%s", status, code)
	}
}

func TestPollStatus_MapsRefund(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"refund"}`))
	}))
	defer server.Close()
	client := relay.NewClient(".")
	client.SetBaseURL(server.URL)
	engine := &Engine{relay: client}

	status, code := engine.PollStatus(context.Background(), "0xreq")
	if status != logcsv.StatusBridgeRefunded || code != logcsv.ErrRefunded {
		t.Fatalf("expected bridge_refunded/REFUNDED, got %s/%s", status, code)
	}
}

func TestPollStatus_PendingOnCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"pending"}`))
	}))
	defer server.Close()
	client := relay.NewClient(".")
	client.SetBaseURL(server.URL)
	engine := &Engine{relay: client}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	status, _ := engine.PollStatus(ctx, "0xreq")
	if status != logcsv.StatusBridgePending {
		t.Fatalf("expected bridge_pending on cancel, got %s", status)
	}
}
