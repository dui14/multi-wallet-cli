package rpc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTransportReusesSameHostTransport(t *testing.T) {
	serverA := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer serverA.Close()
	serverB := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer serverB.Close()
	hostA := strings.TrimPrefix(serverA.URL, "http://")
	hostB := strings.TrimPrefix(serverB.URL, "http://")
	transport := NewTransport()
	client := transport.Client()
	responseA, err := client.Get(serverA.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = responseA.Body.Close()
	transport.mu.Lock()
	created := transport.transports[hostA]
	transport.mu.Unlock()
	if created == nil {
		t.Fatal("expected per-host transport to be cached")
	}
	second, err := client.Get(serverA.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Body.Close()
	transport.mu.Lock()
	again := transport.transports[hostA]
	transport.mu.Unlock()
	if created != again {
		t.Fatal("host transport must be reused across calls")
	}
	third, err := client.Get(serverB.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = third.Body.Close()
	transport.mu.Lock()
	other := transport.transports[hostB]
	transport.mu.Unlock()
	if other == nil || other == created {
		t.Fatal("different hosts must get different transports")
	}
}

func TestTransportWarmPoolsConnection(t *testing.T) {
	var conns int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&conns, 1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	transport := NewTransport()
	if err := transport.Warm(context.Background(), server.URL); err != nil {
		t.Fatal(err)
	}
	client := transport.Client()
	for i := 0; i < 3; i++ {
		response, err := client.Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
	}
	if got := atomic.LoadInt64(&conns); got != 4 {
		t.Fatalf("expected 4 requests, got %d", got)
	}
	transport.mu.Lock()
	_, ok := transport.transports[strings.TrimPrefix(server.URL, "http://")]
	transport.mu.Unlock()
	if !ok {
		t.Fatal("expected host entry after warm")
	}
}

func TestWarmRejectsInvalidURLAndMasksKey(t *testing.T) {
	transport := NewTransport()
	if err := transport.Warm(context.Background(), "not a url?apiKey=secret"); err == nil {
		t.Fatal("expected invalid url error")
	}
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()
	err := transport.Warm(context.Background(), closedURL+"/rpc?apiKey=secret")
	if err == nil {
		t.Fatal("expected warm failure")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("warm error leaked key: %v", err)
	}
}

func TestWarmHonoursContext(t *testing.T) {
	transport := NewTransport()
	slow := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		writer.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := transport.Warm(ctx, slow.URL); err == nil {
		t.Fatal("expected context timeout")
	}
}
