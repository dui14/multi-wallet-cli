package rpc

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiscoveryAcceptPersistsAndLists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rpc_cache.json")
	discovery := NewDiscovery(path, nil, nil)
	discovery.Load()
	if err := discovery.Accept("bsc", Candidate{URL: "https://found.example/rpc", LatencyMs: 120, BlockNumber: 100}); err != nil {
		t.Fatal(err)
	}
	accepted := discovery.Accepted("bsc")
	if len(accepted) != 1 || accepted[0] != "https://found.example/rpc" {
		t.Fatalf("expected accepted endpoint, got %v", accepted)
	}
	reloaded := NewDiscovery(path, nil, nil)
	reloaded.Load()
	accepted = reloaded.Accepted("bsc")
	if len(accepted) != 1 {
		t.Fatalf("expected persisted endpoint, got %v", accepted)
	}
}

func TestDiscoverySkipsUnacceptedAndExpired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rpc_cache.json")
	now := time.Now()
	expired := now.Add(-time.Hour).Format(time.RFC3339)
	content := `{"bsc":[{"url":"https://old.example/rpc","latencyMs":10,"blockNumber":1,"discoveredAt":"` + expired + `","expiresAt":"` + expired + `","acceptedByUser":true},{"url":"https://pending.example/rpc","latencyMs":10,"blockNumber":1,"discoveredAt":"` + now.Format(time.RFC3339) + `","expiresAt":"` + now.Add(time.Hour).Format(time.RFC3339) + `","acceptedByUser":false}]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	discovery := NewDiscovery(path, nil, nil)
	discovery.Load()
	if accepted := discovery.Accepted("bsc"); len(accepted) != 0 {
		t.Fatalf("expected no usable endpoints, got %v", accepted)
	}
}

func TestDiscoveryIgnoresCorruptCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rpc_cache.json")
	if err := os.WriteFile(path, []byte(`{invalid`), 0o600); err != nil {
		t.Fatal(err)
	}
	discovery := NewDiscovery(path, nil, nil)
	discovery.Load()
	if accepted := discovery.Accepted("bsc"); len(accepted) != 0 {
		t.Fatalf("expected empty after corrupt cache, got %v", accepted)
	}
}

func TestDiscoveryFindProbesCandidates(t *testing.T) {
	healthy := httptest.NewServer(methodRouter("0x38", "0x100"))
	defer healthy.Close()
	stale := httptest.NewServer(methodRouter("0x38", "0x10"))
	defer stale.Close()
	discovery := NewDiscovery("", NewProber(healthy.Client()), map[string][]string{"bsc": {healthy.URL, stale.URL}})
	found := discovery.Find(context.Background(), "bsc", 56, 0x100)
	if len(found) != 1 || found[0].URL != healthy.URL {
		t.Fatalf("expected only healthy candidate, got %+v", found)
	}
	if found[0].LatencyMs < 0 {
		t.Fatalf("expected measured latency, got %+v", found[0])
	}
}

func TestDiscoveryFindUsesRegistryPlusSeeds(t *testing.T) {
	server := httptest.NewServer(methodRouter("0x38", "0x100"))
	defer server.Close()
	discovery := NewDiscovery("", NewProber(server.Client()), map[string][]string{"bsc": {server.URL}})
	found := discovery.Find(context.Background(), "bsc", 56, 0x100, server.URL)
	if len(found) != 1 {
		t.Fatalf("expected deduplicated candidate, got %+v", found)
	}
	if strings.TrimSpace(found[0].URL) == "" {
		t.Fatal("candidate URL must not be empty")
	}
}
