package rpc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPoolSelectsPrivateBeforeDiscoveredAndSeed(t *testing.T) {
	pool := NewPool("bsc", false, []string{"https://private.example/rpc"}, []string{"https://discovered.example/rpc"}, []string{"https://seed.example/rpc"})
	got, err := pool.Select()
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://private.example/rpc" {
		t.Fatalf("expected private endpoint first, got %s", got)
	}
}

func TestPoolFallsBackAcrossTiers(t *testing.T) {
	pool := NewPool("bsc", false, []string{"https://private.example/rpc"}, []string{"https://discovered.example/rpc"}, []string{"https://seed.example/rpc"})
	pool.ReportFailure("https://private.example/rpc")
	got, err := pool.Select()
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://discovered.example/rpc" {
		t.Fatalf("expected discovered fallback, got %s", got)
	}
	pool.ReportFailure("https://discovered.example/rpc")
	got, err = pool.Select()
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://seed.example/rpc" {
		t.Fatalf("expected seed fallback, got %s", got)
	}
}

func TestPoolPrivateOnlyNeverFallsBack(t *testing.T) {
	pool := NewPool("bsc", true, []string{"https://private.example/rpc"}, []string{"https://discovered.example/rpc"}, []string{"https://seed.example/rpc"})
	pool.ReportFailure("https://private.example/rpc")
	if _, err := pool.Select(); err == nil {
		t.Fatal("private_only pool must not fall back")
	}
	pool.SetPrivateOnly(false)
	got, err := pool.Select()
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://discovered.example/rpc" {
		t.Fatalf("expected fallback after disabling private_only, got %s", got)
	}
}

func TestPoolRoundRobinAndCooldown(t *testing.T) {
	pool := NewPool("bsc", false, []string{"https://a.example/rpc", "https://b.example/rpc"}, nil, nil)
	first, err := pool.Select()
	if err != nil {
		t.Fatal(err)
	}
	second, err := pool.Select()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("expected round-robin across same tier")
	}
	pool.ReportFailure(first)
	pool.ReportFailure(second)
	if _, err := pool.Select(); err == nil {
		t.Fatal("expected exhaustion while both endpoints cool down")
	}
	pool.now = func() time.Time { return time.Now().Add(61 * time.Second) }
	if _, err := pool.Select(); err != nil {
		t.Fatalf("expected recovery after cooldown, got %v", err)
	}
}

func TestPoolDoRetriesWithinTierBeforeNextTier(t *testing.T) {
	pool := NewPool("bsc", false, []string{"https://private.example/rpc"}, []string{"https://d1.example/rpc", "https://d2.example/rpc"}, []string{"https://seed.example/rpc"})
	calls := 0
	err := pool.Do(context.Background(), func(_ context.Context, rawURL string) error {
		calls++
		if strings.Contains(rawURL, "private") {
			return fmt.Errorf("boom")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expected one private try plus one discovered try, got %d", calls)
	}
}

func TestPoolErrorMasksSecrets(t *testing.T) {
	pool := NewPool("bsc", false, []string{"https://private.example/rpc?apiKey=secret-value"}, nil, nil)
	err := pool.Do(context.Background(), func(_ context.Context, _ string) error {
		return fmt.Errorf("boom")
	})
	if err == nil {
		t.Fatal("expected failure")
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("error must not leak api key: %v", err)
	}
}

func TestPoolSkipsWebSocketEndpoints(t *testing.T) {
	pool := NewSeedPool("linea-mainnet", []string{"wss://linea-rpc.publicnode.com", "https://linea-rpc.publicnode.com"})
	var tried []string
	err := pool.Do(context.Background(), func(_ context.Context, rawURL string) error {
		tried = append(tried, rawURL)
		return fmt.Errorf("boom")
	})
	if err == nil {
		t.Fatal("expected failure once the only http endpoint fails")
	}
	if len(tried) != 1 || tried[0] != "https://linea-rpc.publicnode.com" {
		t.Fatalf("expected only the http endpoint to be tried, got %v", tried)
	}
	if !strings.Contains(err.Error(), "wss://linea-rpc.publicnode.com") {
		t.Fatalf("expected exhausted error to list the skipped websocket endpoint, got %v", err)
	}
}

func TestPoolWebSocketOnlyChainReportsNoHealthyEndpoint(t *testing.T) {
	pool := NewSeedPool("linea-mainnet", []string{"wss://linea-rpc.publicnode.com"})
	got, err := pool.Select()
	if err == nil {
		t.Fatalf("expected exhaustion for websocket-only chain, got %s", got)
	}
	if !strings.Contains(err.Error(), "no healthy endpoint") {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := pool.Do(context.Background(), func(context.Context, string) error { return nil }); err == nil {
		t.Fatal("expected Do to fail when every endpoint is a websocket URL")
	}
}

func TestPoolRaceTakesFirstSuccess(t *testing.T) {
	pool := NewSeedPool("base", []string{"https://slow.example/rpc", "https://fast.example/rpc"})
	err := pool.Race(context.Background(), func(_ context.Context, rawURL string) error {
		if strings.Contains(rawURL, "slow") {
			time.Sleep(200 * time.Millisecond)
			return fmt.Errorf("timeout")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPoolDoSkipsNotReadyWithoutCooldown(t *testing.T) {
	pool := NewSeedPool("base", []string{"https://lagging.example/rpc", "https://live.example/rpc"})
	var tried []string
	err := pool.Do(context.Background(), func(_ context.Context, rawURL string) error {
		tried = append(tried, rawURL)
		if strings.Contains(rawURL, "lagging") {
			return ErrNotReady
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tried) != 2 || tried[0] != "https://lagging.example/rpc" || tried[1] != "https://live.example/rpc" {
		t.Fatalf("expected lagging then live, got %v", tried)
	}
	next, err := pool.Select()
	if err != nil {
		t.Fatal(err)
	}
	if next != "https://lagging.example/rpc" {
		t.Fatalf("pending endpoint must stay eligible, next = %s", next)
	}
}

func TestPoolDoAllNotReady(t *testing.T) {
	pool := NewSeedPool("base", []string{"https://a.example/rpc", "https://b.example/rpc"})
	err := pool.Do(context.Background(), func(context.Context, string) error {
		return ErrNotReady
	})
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("expected ErrNotReady, got %v", err)
	}
	if _, err := pool.Select(); err != nil {
		t.Fatalf("pending endpoints must not enter cooldown: %v", err)
	}
}

func TestPoolDoRotatesThroughEveryEndpoint(t *testing.T) {
	pool := NewSeedPool("linea-mainnet", []string{"https://dead.example/rpc", "https://live.example/rpc"})
	calls := 0
	err := pool.Do(context.Background(), func(_ context.Context, rawURL string) error {
		calls++
		if strings.Contains(rawURL, "dead") {
			return fmt.Errorf("unreachable")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expected rotation through both endpoints, got %d calls", calls)
	}
	next, err := pool.Select()
	if err != nil {
		t.Fatal(err)
	}
	if next != "https://live.example/rpc" {
		t.Fatalf("failed endpoint must be parked in cooldown, next selection = %s", next)
	}
}

func TestPoolDoFailsOverToHealthyEndpoint(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x64"}`))
	}))
	defer healthy.Close()
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	pool := NewSeedPool("linea-mainnet", []string{"wss://linea-rpc.publicnode.com", deadURL, healthy.URL})
	var used string
	err := pool.Do(context.Background(), func(ctx context.Context, rawURL string) error {
		used = rawURL
		request, buildErr := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"eth_getBalance","params":[]}`))
		if buildErr != nil {
			return buildErr
		}
		request.Header.Set("Content-Type", "application/json")
		response, doErr := healthy.Client().Do(request)
		if doErr != nil {
			return doErr
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("http %d", response.StatusCode)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if used != healthy.URL {
		t.Fatalf("expected failover to the healthy endpoint, got %s", used)
	}
}
