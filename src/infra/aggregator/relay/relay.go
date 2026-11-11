package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://api.relay.link"
	quotePath      = "/quote/v2"
	chainsPath     = "/chains"
	statusPath     = "/intents/status/v3"
	apiKeyEnv      = "RELAY_API_KEY"
	requestTimeout = 30 * time.Second
)

// NativeSentinel is the Relay address that denotes a chain's native coin.
const NativeSentinel = "0x0000000000000000000000000000000000000000"

// QuoteRequest is the POST /quote/v2 body. One request covers same-chain swap,
// cross-chain bridge, and cross-chain bridge+swap depending on the chain and
// currency fields.
type QuoteRequest struct {
	User                string `json:"user"`
	OriginChainID       int64  `json:"originChainId"`
	DestinationChainID  int64  `json:"destinationChainId"`
	OriginCurrency      string `json:"originCurrency"`
	DestinationCurrency string `json:"destinationCurrency"`
	Amount              string `json:"amount"`
	TradeType           string `json:"tradeType"`
	Recipient           string `json:"recipient,omitempty"`
	SlippageTolerance   string `json:"slippageTolerance,omitempty"`
}

// StepItemData carries the signable fields Relay computed for one transaction.
type StepItemData struct {
	To           string `json:"to"`
	Data         string `json:"data"`
	Value        string `json:"value"`
	ChainID      int64  `json:"chainId"`
	Gas          string `json:"gas,omitempty"`
	MaxFeePerGas string `json:"maxFeePerGas,omitempty"`
}

// StepItem is one actionable item inside a step.
type StepItem struct {
	Status string       `json:"status"`
	Data   StepItemData `json:"data"`
}

// Step is an ordered signable stage; an approval step may precede the main one.
type Step struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	RequestID string     `json:"requestId"`
	Items     []StepItem `json:"items"`
}

// Fees reports Relay's gas and relayer fee breakdown for a quote.
type Fees struct {
	Gas     string `json:"gas,omitempty"`
	Relayer string `json:"relayer,omitempty"`
}

// Details surfaces the expected output amount for the confirmation summary.
type Details struct {
	Currency        Currency `json:"currency"`
	Amount          string   `json:"amount,omitempty"`
	AmountFormatted string   `json:"amountFormatted,omitempty"`
}

// Currency identifies a token involved in a quote.
type Currency struct {
	Symbol   string `json:"symbol,omitempty"`
	Address  string `json:"address,omitempty"`
	Decimals int    `json:"decimals,omitempty"`
}

// QuoteResponse is the subset of POST /quote/v2 the tool consumes.
type QuoteResponse struct {
	RequestID string  `json:"requestId"`
	Steps     []Step  `json:"steps"`
	Fees      Fees    `json:"fees"`
	Details   Details `json:"details"`
}

// Chain describes a Relay-supported chain and its virtual machine type.
type Chain struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	VMType string `json:"vmType"`
}

// Status is a normalized Relay intent lifecycle value.
type Status string

const (
	StatusWaiting    Status = "waiting"
	StatusDepositing Status = "depositing"
	StatusPending    Status = "pending"
	StatusSuccess    Status = "success"
	StatusFailure    Status = "failure"
	StatusRefund     Status = "refund"
)

// StatusResponse is the GET /intents/status/v3 payload.
type StatusResponse struct {
	Status             Status   `json:"status"`
	InTxHashes         []string `json:"inTxHashes"`
	TxHashes           []string `json:"txHashes"`
	OriginChainID      int64    `json:"originChainId"`
	DestinationChainID int64    `json:"destinationChainId"`
}

// Client is a thin, context-aware Relay Protocol HTTP client.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient builds a Relay client. It loads .env for RELAY_API_KEY when the
// variable is not already exported. An empty key still works at Relay's lower
// unauthenticated rate limit.
func NewClient(baseDir string) *Client {
	loadEnvFile(baseDir + string(os.PathSeparator) + ".env")
	base := strings.TrimSpace(os.Getenv("RELAY_BASE_URL"))
	if base == "" {
		base = defaultBaseURL
	}
	return &Client{
		baseURL:    strings.TrimRight(base, "/"),
		apiKey:     strings.TrimSpace(os.Getenv(apiKeyEnv)),
		httpClient: &http.Client{Timeout: requestTimeout},
	}
}

// SetBaseURL overrides the API base URL. It is primarily used by tests to point
// the client at a local server.
func (c *Client) SetBaseURL(base string) {
	c.baseURL = strings.TrimRight(strings.TrimSpace(base), "/")
}

// Quote requests a route and its ordered signable steps.
func (c *Client) Quote(ctx context.Context, request QuoteRequest) (*QuoteResponse, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode quote request: %w", err)
	}
	var response QuoteResponse
	if err := c.post(ctx, quotePath, body, &response); err != nil {
		return nil, err
	}
	if len(response.Steps) == 0 {
		return nil, fmt.Errorf("relay returned no route steps")
	}
	return &response, nil
}

// Chains lists Relay-supported chains with their virtual machine type.
func (c *Client) Chains(ctx context.Context) ([]Chain, error) {
	var chains []Chain
	if err := c.get(ctx, chainsPath, &chains); err != nil {
		return nil, err
	}
	return chains, nil
}

// Status polls the intent lifecycle for a quote's requestId.
func (c *Client) Status(ctx context.Context, requestID string) (*StatusResponse, error) {
	var response StatusResponse
	if err := c.get(ctx, statusPath+"?requestId="+requestID, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) get(ctx context.Context, path string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build relay request: %w", err)
	}
	return c.do(request, target)
}

func (c *Client) post(ctx context.Context, path string, body []byte, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build relay request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	return c.do(request, target)
}

func (c *Client) do(request *http.Request, target any) error {
	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("relay request failed: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read relay response: %w", err)
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("relay rate limited")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("relay returned HTTP %d", response.StatusCode)
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("decode relay response: %w", err)
	}
	return nil
}
