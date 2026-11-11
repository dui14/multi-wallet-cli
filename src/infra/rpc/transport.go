package rpc

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Shared transport tuning.
const (
	defaultKeepAlive    = 30 * time.Second
	maxIdleConns        = 64
	maxIdleConnsPerHost = 8
	warmTimeout         = 5 * time.Second
)

// Transport is a shared keep-alive HTTP transport, one idle pool per host.
type Transport struct {
	mu         sync.Mutex
	transports map[string]*http.Transport
	client     *http.Client
	now        func() time.Time
}

// NewTransport builds the shared transport stack for RPC calls.
func NewTransport() *Transport {
	transport := &Transport{
		transports: map[string]*http.Transport{},
		now:        time.Now,
	}
	transport.client = &http.Client{Transport: transport}
	return transport
}

// Client exposes the shared HTTP client bound to this transport.
func (t *Transport) Client() *http.Client {
	return t.client
}

// RoundTrip routes each request through a per-host keep-alive transport.
func (t *Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	hostTransport := t.forHost(request.URL.Host)
	cloned := request.Clone(request.Context())
	cloned.Header = request.Header.Clone()
	return hostTransport.RoundTrip(cloned)
}

func (t *Transport) forHost(host string) *http.Transport {
	t.mu.Lock()
	defer t.mu.Unlock()
	existing, ok := t.transports[host]
	if ok {
		return existing
	}
	created := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: defaultKeepAlive,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          maxIdleConns,
		MaxIdleConnsPerHost:   maxIdleConnsPerHost,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	t.transports[host] = created
	return created
}

// Warm dials the host behind rawURL so later calls reuse the connection.
func (t *Transport) Warm(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("invalid warm url")
	}
	warmCtx, cancel := context.WithTimeout(ctx, warmTimeout)
	defer cancel()
	hostTransport := t.forHost(parsed.Host)
	request, err := http.NewRequestWithContext(warmCtx, http.MethodHead, parsed.Scheme+"://"+parsed.Host+"/", nil)
	if err != nil {
		return fmt.Errorf("build warm request: %w", err)
	}
	response, err := hostTransport.RoundTrip(request)
	if err != nil {
		return fmt.Errorf("warm %s failed", maskEndpoint(rawURL))
	}
	_ = response.Body.Close()
	return nil
}
