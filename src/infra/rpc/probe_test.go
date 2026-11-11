package rpc

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func methodRouter(chainHex string, blockHex string) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		_ = request.Body.Close()
		writer.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "eth_chainId") {
			_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"` + chainHex + `"}`))
			return
		}
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"` + blockHex + `"}`))
	})
}

func TestProbeEVMHealthy(t *testing.T) {
	server := httptest.NewServer(methodRouter("0x38", "0x100"))
	defer server.Close()
	prober := NewProber(server.Client())
	result := prober.ProbeEVM(context.Background(), server.URL, 56, 0x100)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if !result.ChainOK || result.BehindBlocks != 0 || result.RateLimited {
		t.Fatalf("unexpected probe result: %+v", result)
	}
	if result.Latency < 0 || result.Latency > 5*time.Second {
		t.Fatalf("latency out of range: %v", result.Latency)
	}
}

func TestProbeEVMChainMismatch(t *testing.T) {
	server := httptest.NewServer(methodRouter("0x1", "0x100"))
	defer server.Close()
	prober := NewProber(server.Client())
	result := prober.ProbeEVM(context.Background(), server.URL, 56, 0x100)
	if result.Err == nil || result.ChainOK {
		t.Fatal("expected chain mismatch")
	}
}

func TestProbeEVMLagging(t *testing.T) {
	server := httptest.NewServer(methodRouter("0x38", "0xF0"))
	defer server.Close()
	prober := NewProber(server.Client())
	result := prober.ProbeEVM(context.Background(), server.URL, 56, 0x100)
	if result.Err == nil {
		t.Fatal("expected lag error")
	}
	if result.BehindBlocks != 16 {
		t.Fatalf("expected 16 blocks behind, got %d", result.BehindBlocks)
	}
}

func TestProbeEVMRateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(429)
	}))
	defer server.Close()
	prober := NewProber(server.Client())
	result := prober.ProbeEVM(context.Background(), server.URL, 56, 0)
	if result.Err == nil || !result.RateLimited {
		t.Fatalf("expected rate-limited probe, got %+v", result)
	}
}

func TestProbeSolanaHealthyAndLagging(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body := make([]byte, request.ContentLength)
		_, _ = request.Body.Read(body)
		_ = request.Body.Close()
		writer.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "getHealth") {
			_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"ok"}`))
			return
		}
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":90}`))
	}))
	defer server.Close()
	prober := NewProber(server.Client())
	healthy := prober.ProbeSolana(context.Background(), server.URL, 90)
	if healthy.Err != nil {
		t.Fatal(healthy.Err)
	}
	lagging := prober.ProbeSolana(context.Background(), server.URL, 100)
	if lagging.Err == nil || lagging.BehindBlocks != 10 {
		t.Fatalf("expected slot lag, got %+v", lagging)
	}
}

func TestProbeErrorMasksAPIKey(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	rpcURL := closed.URL + "/rpc?apiKey=super-secret"
	closed.Close()
	prober := NewProber(nil)
	result := prober.ProbeEVM(context.Background(), rpcURL, 56, 0)
	if result.Err == nil {
		t.Fatal("expected error for unreachable host")
	}
	if strings.Contains(result.Err.Error(), "super-secret") {
		t.Fatalf("probe error leaked key: %v", result.Err)
	}
}
