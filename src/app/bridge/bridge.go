package bridge

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

// Request describes a cross-chain bridge for one source wallet. When FromToken
// and ToToken differ it is a bridge+swap; otherwise it is a same-asset bridge.
type Request struct {
	FromChain    string
	ToChain      string
	From         wallet.PublicWallet
	FromToken    config.Token
	ToToken      config.Token
	Amount       *big.Int
	Recipient    string
	Slippage     string
	FromIsNative bool
	ToIsNative   bool
}

// Quote is the reviewed cross-chain route shown before confirmation.
type Quote struct {
	RequestID       string
	ExpectedReceive string
	ReceiveSymbol   string
	GasFee          string
	RelayerFee      string
	CrossAsset      bool
}

// Result aggregates one wallet's bridge outcome for the report and log.
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

// NewService builds the bridge service over the shared Relay execution engine.
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

// SupportedChains returns EVM chain slugs Relay can bridge, sorted for display.
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

// Quote fetches and reviews a cross-chain route for one wallet.
func (s *Service) Quote(ctx context.Context, request Request) (*Quote, error) {
	origin, dest, err := s.resolveNetworks(request)
	if err != nil {
		return nil, err
	}
	if err := validateBridge(request); err != nil {
		return nil, err
	}
	response, err := s.engine.Relay().Quote(ctx, buildBridgeQuote(origin, dest, request))
	if err != nil {
		return nil, err
	}
	return &Quote{
		RequestID:       response.RequestID,
		ExpectedReceive: response.Details.Amount,
		ReceiveSymbol:   response.Details.Currency.Symbol,
		GasFee:          response.Fees.Gas,
		RelayerFee:      response.Fees.Relayer,
		CrossAsset:      request.FromToken.Address != request.ToToken.Address,
	}, nil
}

// Do executes a reviewed bridge: re-fetch the quote to avoid a stale route,
// sign each step in order, broadcast, then poll the cross-chain intent.
func (s *Service) Do(ctx context.Context, request Request) Result {
	origin, dest, netErr := s.resolveNetworks(request)
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
	if netErr != nil {
		base.FinalStatus = logcsv.StatusSkipped
		base.ErrorCode = logcsv.ErrNoRoute
		base.Err = netErr
		return base
	}
	if err := validateBridge(request); err != nil {
		base.FinalStatus = logcsv.StatusSkipped
		base.ErrorCode = logcsv.ErrNoRoute
		base.Err = err
		return base
	}
	response, err := s.engine.Relay().Quote(ctx, buildBridgeQuote(origin, dest, request))
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
	sourceHash, status, code := s.engine.Execute(ctx, origin, key, request.From, response)
	base.SourceTxHash = sourceHash
	base.FinalStatus = status
	base.ErrorCode = code
	s.engine.Log(s.logger, logcsv.Event{
		TxHash:         sourceHash,
		Mode:           "bridge",
		Chain:          request.FromChain,
		WalletFrom:     request.From.Label,
		WalletTo:       request.Recipient,
		Token:          request.ToToken.Symbol,
		TokenAddress:   request.ToToken.Address,
		AmountSent:     sent,
		AmountReceived: response.Details.Amount,
		Status:         status,
		Error:          code,
		ExplorerURL:    explorerURL(origin, sourceHash),
	})
	return base
}

func (s *Service) resolveNetworks(request Request) (config.Network, config.Network, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	origin, ok := s.networks[request.FromChain]
	if !ok {
		return config.Network{}, config.Network{}, fmt.Errorf("source chain %q not found", request.FromChain)
	}
	dest, ok := s.networks[request.ToChain]
	if !ok {
		return config.Network{}, config.Network{}, fmt.Errorf("destination chain %q not found", request.ToChain)
	}
	return origin, dest, nil
}

func validateBridge(request Request) error {
	if request.FromChain == request.ToChain {
		return fmt.Errorf("bridge requires different source and destination chains")
	}
	if request.Amount == nil || request.Amount.Sign() <= 0 {
		return fmt.Errorf("amount must be greater than zero")
	}
	return nil
}

func buildBridgeQuote(origin config.Network, dest config.Network, request Request) relay.QuoteRequest {
	return relay.QuoteRequest{
		User:                request.From.Address,
		OriginChainID:       origin.RelayChainID,
		DestinationChainID:  dest.RelayChainID,
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
