package swap

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"sync"

	"multi-wallet-cli/app/relayexec"
	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/aggregator/relay"
	"multi-wallet-cli/infra/config"
	"multi-wallet-cli/infra/logcsv"
)

// Request describes a same-chain token swap for one source wallet.
type Request struct {
	Chain        string
	From         wallet.PublicWallet
	FromToken    config.Token
	ToToken      config.Token
	Amount       *big.Int
	Recipient    string
	Slippage     string
	FromIsNative bool
	ToIsNative   bool
}

// Quote is the reviewed route shown to the user before confirmation.
type Quote struct {
	RequestID       string
	ExpectedReceive string
	ReceiveSymbol   string
	GasFee          string
	RelayerFee      string
	Steps           int
}

// Result aggregates one wallet's swap outcome for the report and log.
type Result struct {
	Wallet       string
	RequestID    string
	SourceTxHash string
	FinalStatus  logcsv.Status
	ErrorCode    string
	AmountSent   string
	AmountRecv   string
	Token        string
	TokenAddress string
	Err          error
}

type Service struct {
	mu       sync.RWMutex
	networks map[string]config.Network
	tokens   map[string]map[string]config.Token
	engine   *relayexec.Engine
	logger   *logcsv.Writer
}

// NewService builds the swap service over the shared Relay execution engine.
func NewService(networks map[string]config.Network, tokens map[string]map[string]config.Token, engine *relayexec.Engine, logger *logcsv.Writer) *Service {
	return &Service{networks: networks, tokens: tokens, engine: engine, logger: logger}
}

// Networks exposes the loaded chain registry for CLI pickers.
func (s *Service) Networks() map[string]config.Network {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.networks
}

// Tokens exposes the loaded token registry for CLI pickers.
func (s *Service) Tokens() map[string]map[string]config.Token {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tokens
}

// SupportedChains returns EVM chain slugs Relay can route, sorted for display.
func (s *Service) SupportedChains() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	slugs := make([]string, 0, len(s.networks))
	for slug, network := range s.networks {
		if network.VMType == "evm" {
			slugs = append(slugs, slug)
		}
	}
	sort.Strings(slugs)
	return slugs
}

// Quote fetches and reviews a same-chain route for one wallet.
func (s *Service) Quote(ctx context.Context, request Request) (*Quote, error) {
	network, ok := s.networks[request.Chain]
	if !ok {
		return nil, fmt.Errorf("chain %q not found", request.Chain)
	}
	if err := validateSwapTokens(request); err != nil {
		return nil, err
	}
	response, err := s.engine.Relay().Quote(ctx, buildSwapQuote(network, request))
	if err != nil {
		return nil, err
	}
	return &Quote{
		RequestID:       response.RequestID,
		ExpectedReceive: response.Details.Amount,
		ReceiveSymbol:   response.Details.Currency.Symbol,
		GasFee:          response.Fees.Gas,
		RelayerFee:      response.Fees.Relayer,
		Steps:           len(response.Steps),
	}, nil
}

// Do executes a reviewed same-chain swap: it re-fetches the quote to avoid a
// stale route, signs each step in order, broadcasts, then polls final status.
func (s *Service) Do(ctx context.Context, request Request) Result {
	network, ok := s.networks[request.Chain]
	if !ok {
		return Result{Wallet: request.From.Label, FinalStatus: logcsv.StatusSkipped, ErrorCode: logcsv.ErrNoRoute, Err: fmt.Errorf("chain %q not found", request.Chain)}
	}
	sent := "0"
	if request.Amount != nil {
		sent = request.Amount.String()
	}
	base := Result{
		Wallet:       request.From.Label,
		AmountSent:   sent,
		Token:        request.FromToken.Symbol,
		TokenAddress: request.FromToken.Address,
	}
	if err := validateSwapTokens(request); err != nil {
		base.FinalStatus = logcsv.StatusSkipped
		base.ErrorCode = logcsv.ErrNoRoute
		base.Err = err
		return base
	}
	response, err := s.engine.Relay().Quote(ctx, buildSwapQuote(network, request))
	if err != nil {
		base.FinalStatus = logcsv.StatusSkipped
		base.ErrorCode = relayexec.NormalizeError(err)
		base.Err = err
		return base
	}
	base.RequestID = response.RequestID
	base.AmountRecv = response.Details.Amount
	key, ok := s.engine.Signer(request.From.Label)
	if !ok {
		base.FinalStatus = logcsv.StatusSkipped
		base.ErrorCode = logcsv.ErrSigningFailed
		base.Err = fmt.Errorf("signing key unavailable")
		return base
	}
	sourceHash, status, code := s.engine.Execute(ctx, network, key, request.From, response)
	base.SourceTxHash = sourceHash
	base.FinalStatus = status
	base.ErrorCode = code
	s.engine.Log(s.logger, logcsv.Event{
		TxHash:         sourceHash,
		Mode:           "swap",
		Chain:          request.Chain,
		WalletFrom:     request.From.Label,
		WalletTo:       request.Recipient,
		Token:          request.ToToken.Symbol,
		TokenAddress:   request.ToToken.Address,
		AmountSent:     sent,
		AmountReceived: response.Details.Amount,
		Status:         status,
		Error:          code,
		ExplorerURL:    explorerURL(network, sourceHash),
	})
	return base
}

func validateSwapTokens(request Request) error {
	if request.Amount == nil || request.Amount.Sign() <= 0 {
		return fmt.Errorf("amount must be greater than zero")
	}
	if request.FromToken.Address == request.ToToken.Address {
		return fmt.Errorf("from-token and to-token must differ")
	}
	return nil
}

func buildSwapQuote(network config.Network, request Request) relay.QuoteRequest {
	return relay.QuoteRequest{
		User:                request.From.Address,
		OriginChainID:       network.RelayChainID,
		DestinationChainID:  network.RelayChainID,
		OriginCurrency:      currencyOf(request.FromToken, request.FromIsNative),
		DestinationCurrency: currencyOf(request.ToToken, request.ToIsNative),
		Amount:              request.Amount.String(),
		TradeType:           "EXACT_INPUT",
		Recipient:           request.Recipient,
		SlippageTolerance:   request.Slippage,
	}
}

func currencyOf(token config.Token, native bool) string {
	if native || token.Address == "" {
		return relay.NativeSentinel
	}
	return token.Address
}

func explorerURL(network config.Network, txHash string) string {
	if txHash == "" || network.Explorer == "" {
		return ""
	}
	return trimSlash(network.Explorer) + "/tx/" + txHash
}

func trimSlash(value string) string {
	for len(value) > 0 && value[len(value)-1] == '/' {
		value = value[:len(value)-1]
	}
	return value
}
