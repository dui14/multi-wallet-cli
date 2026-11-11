package rpc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBlastEVMRequiresConfiguration(t *testing.T) {
	blast := NewBlast(nil, "", "")
	if _, err := blast.BroadcastEVM(context.Background(), "https://primary.example", "0xabc"); !errors.Is(err, ErrBlastNotConfigured) {
		t.Fatalf("expected ErrBlastNotConfigured, got %v", err)
	}
	blastSol := NewBlast(nil, "https://evm.example", "")
	if _, err := blastSol.BroadcastSolana(context.Background(), "https://primary.example", "AAAA"); !errors.Is(err, ErrBlastNotConfigured) {
		t.Fatalf("expected ErrBlastNotConfigured, got %v", err)
	}
}

func TestBlastEVMTakesFirstSuccess(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(1500 * time.Millisecond)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0xslowhash"}`))
	}))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0xfasthash"}`))
	}))
	defer fast.Close()
	blast := NewBlast(fast.Client(), fast.URL, "")
	start := time.Now()
	hash, err := blast.BroadcastEVM(context.Background(), slow.URL, "0xdeadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if hash != "0xfasthash" {
		t.Fatalf("expected first success hash, got %s", hash)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("expected fast path to win, took %v", elapsed)
	}
}

func TestBlastEVMBothFailReportsMaskedError(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer failing.Close()
	blast := NewBlast(failing.Client(), failing.URL+"?apiKey=topsecret", "")
	_, err := blast.BroadcastEVM(context.Background(), failing.URL+"?apiKey=primarysecret", "0xdeadbeef")
	if err == nil {
		t.Fatal("expected aggregate error")
	}
	if strings.Contains(err.Error(), "topsecret") || strings.Contains(err.Error(), "primarysecret") {
		t.Fatalf("blast error leaked keys: %v", err)
	}
	if !strings.Contains(err.Error(), "both paths") {
		t.Fatalf("expected aggregate failure message, got %v", err)
	}
}

func TestBlastEVMErrorsAreMasked(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	primaryURL := closed.URL + "/rpc?apiKey=primarykey"
	blastURL := closed.URL + "/rpc?apiKey=blastkey"
	closed.Close()
	blast := NewBlast(nil, blastURL, "")
	_, err := blast.BroadcastEVM(context.Background(), primaryURL, "0xdeadbeef")
	if err == nil {
		t.Fatal("expected failure")
	}
	if strings.Contains(err.Error(), "blastkey") || strings.Contains(err.Error(), "primarykey") {
		t.Fatalf("blast error leaked keys: %v", err)
	}
}

func TestBlastSolanaTakesFirstSuccess(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"failed"}}`))
	}))
	defer failing.Close()
	healthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body := make([]byte, request.ContentLength)
		_, _ = request.Body.Read(body)
		_ = request.Body.Close()
		writer.Header().Set("Content-Type", "application/json")
		if !strings.Contains(string(body), "sendTransaction") {
			_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"unsupported"}}`))
			return
		}
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"sigABC"}`))
	}))
	defer healthy.Close()
	blast := NewBlast(healthy.Client(), "", healthy.URL)
	signature, err := blast.BroadcastSolana(context.Background(), failing.URL, "AAAA")
	if err != nil {
		t.Fatal(err)
	}
	if signature != "sigABC" {
		t.Fatalf("expected signature from healthy path, got %s", signature)
	}
}

func TestBlastNeverRetriesAThirdTime(t *testing.T) {
	var calls int64
	counter := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&calls, 1)
		writer.WriteHeader(http.StatusInternalServerError)
	})
	server := httptest.NewServer(counter)
	defer server.Close()
	blast := NewBlast(server.Client(), server.URL, "")
	_, err := blast.BroadcastEVM(context.Background(), server.URL, "0xdead")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := atomic.LoadInt64(&calls); got > 2 {
		t.Fatalf("expected at most 2 attempts, got %d", got)
	}
}
