package transfer

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/config"
	"multi-wallet-cli/infra/evm"
	"multi-wallet-cli/infra/logcsv"
	"multi-wallet-cli/infra/rpc"
)

type Service struct {
	evm        *evm.Client
	mu         sync.RWMutex
	networks   map[string]config.Network
	tokens     map[string]map[string]config.Token
	pools      map[string]*rpc.Pool
	logger     *logcsv.Writer
	thresholds config.TransferSettings
}

func NewService(baseDir string) (*Service, error) {
	networks, err := config.LoadNetworks(baseDir)
	if err != nil {
		return nil, err
	}
	tokens, err := config.LoadTokens(baseDir)
	if err != nil {
		return nil, err
	}
	thresholds, err := config.LoadTransferSettings(baseDir)
	if err != nil {
		return nil, err
	}
	return &Service{evm: evm.NewClient(), networks: networks, tokens: tokens, pools: buildPools(networks), thresholds: thresholds}, nil
}

// buildPools creates one seed-tier rotation pool per network so a failing
// endpoint rotates to the remaining ones instead of aborting the request.
func buildPools(networks map[string]config.Network) map[string]*rpc.Pool {
	pools := make(map[string]*rpc.Pool, len(networks))
	for slug, network := range networks {
		pools[slug] = rpc.NewSeedPool(slug, network.SeedEndpoints())
	}
	return pools
}

func (s *Service) pool(chain string) *rpc.Pool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pools[chain]
}

// Reload re-reads networks.json and tokens.json so networks and tokens added or
// removed in Config are reflected across the whole session without unlocking
// again. Rotation pools are rebuilt so newly added endpoints join rotation.
func (s *Service) Reload() error {
	networks, err := config.LoadNetworks(".")
	if err != nil {
		return err
	}
	tokens, err := config.LoadTokens(".")
	if err != nil {
		return err
	}
	thresholds, err := config.LoadTransferSettings(".")
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.networks = networks
	s.tokens = tokens
	s.pools = buildPools(networks)
	s.thresholds = thresholds
	s.mu.Unlock()
	return nil
}

// Flow carries per-request chain state used by the plan builders and executor.
type Flow struct {
	evm           *evm.Client
	pool          *rpc.Pool
	network       config.Network
	ok            bool
	rpc           string
	chainID       int64
	from          wallet.PublicWallet
	froms         []wallet.PublicWallet
	tokens        map[string]config.Token
	fees          *evm.FeeEstimate
	nonces        map[string]uint64
	balances      map[string]*big.Int
	meta          map[string]TokenLine
	gasLimit      uint64
	blockGas      uint64
	signer        KeySigner
	minCalls      int
	minDelegated  int
	executor      string
	executorHash  string
	chainReady    bool
	runtimeHash   string
	capabilitySet bool
	capabilityErr error
	logger        *logcsv.Writer
	logBatch      string
	logMode       string
	logChain      string
	mu            sync.Mutex
}

func (s *Service) NewFlow(chain string) *Flow {
	s.mu.RLock()
	network, ok := s.networks[chain]
	chainTokens := s.tokens[chain]
	pool := s.pools[chain]
	logger := s.logger
	thresholds := s.thresholds
	s.mu.RUnlock()
	flow := &Flow{
		evm:          s.evm,
		pool:         pool,
		network:      network,
		ok:           ok,
		tokens:       chainTokens,
		executor:     strings.TrimSpace(network.Contracts.EIP7702Executor),
		executorHash: strings.TrimSpace(network.Contracts.EIP7702ExecutorCodeHash),
		minCalls:     thresholds.EIP7702MinCalls,
		minDelegated: thresholds.EIP7702MinCallsWhenDelegated,
		chainID:      network.ChainID,
		nonces:       map[string]uint64{},
		balances:     map[string]*big.Int{},
		meta:         map[string]TokenLine{},
		logger:       logger,
		logChain:     chain,
	}
	if ok {
		flow.rpc = network.PrimaryRPC()
	}
	return flow
}

// call rotates across the chain's healthy endpoints until call succeeds,
// failing over when an endpoint is unreachable.
func (f *Flow) call(ctx context.Context, call func(ctx context.Context, rpcURL string) error) error {
	if f.pool == nil {
		return call(ctx, f.rpc)
	}
	return f.pool.Do(ctx, call)
}

func (f *Flow) race(ctx context.Context, call func(ctx context.Context, rpcURL string) error) error {
	if f.pool == nil {
		return call(ctx, f.rpc)
	}
	return f.pool.Race(ctx, call)
}

func (f *Flow) liveRPCs() []string {
	if f.pool != nil {
		if urls := f.pool.LiveURLs(); len(urls) > 0 {
			return urls
		}
	}
	if strings.TrimSpace(f.rpc) == "" {
		return nil
	}
	return []string{f.rpc}
}

func (f *Flow) ChainID() int64        { return f.chainID }
func (f *Flow) EIP1559() bool         { return f.network.EIP1559 }
func (f *Flow) RPC() string           { return f.rpc }
func (f *Flow) Explorer() string      { return f.network.Explorer }
func (f *Flow) NativeSymbol() string  { return f.network.NativeSymbol }
func (f *Flow) NativeDecimals() uint8 { return f.network.NativeDecimals }
func (s *Service) Networks() map[string]config.Network {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.networks
}
func (s *Service) Tokens() map[string]map[string]config.Token {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tokens
}

// BalancesFor returns a chain flow bound to one owner for balance lookups.
func (s *Service) BalancesFor(chain string, owner wallet.PublicWallet) *Flow {
	flow := s.NewFlow(chain)
	flow.from = owner
	return flow
}

// TokenBalance reads one token balance for the flow's owner. An empty address reads the native balance.
func (f *Flow) TokenBalance(ctx context.Context, address string) (*big.Int, error) {
	item := TokenLine{Address: address}
	if strings.TrimSpace(address) == "" {
		item.Native = true
	}
	return f.balanceOf(ctx, f.from.Address, item)
}

func (f *Flow) KnownTokens() map[string]config.Token { return f.tokens }

// ResolveToken reads a custom token's symbol and decimals on-chain and flags it unverified.
func (s *Service) ResolveToken(ctx context.Context, chain string, address string) (TokenLine, error) {
	flow := s.NewFlow(chain)
	if flow.RPC() == "" {
		return TokenLine{}, fmt.Errorf("chain not found")
	}
	return flow.loadMeta(ctx, TokenLine{Address: address, Custom: true})
}

// IsContract reports whether address holds contract code on the given chain.
func (s *Service) IsContract(ctx context.Context, chain string, address string) bool {
	flow := s.NewFlow(chain)
	if flow.RPC() == "" {
		return false
	}
	normalized := normalizeEVMAddress(address)
	if normalized == "" {
		return false
	}
	hasCode := false
	if err := flow.call(ctx, func(callCtx context.Context, rpcURL string) error {
		var codeErr error
		hasCode, codeErr = flow.evm.HasCode(callCtx, rpcURL, normalized)
		return codeErr
	}); err != nil {
		return false
	}
	return hasCode
}

func (f *Flow) prepareEVM(item wallet.PublicWallet) error {
	if !f.ok {
		return fmt.Errorf("chain not found")
	}
	if item.ChainKind != wallet.ChainKindEVM {
		return fmt.Errorf("wallet %q is not an EVM wallet", item.Label)
	}
	f.from = item
	return nil
}

func (f *Flow) emptyPlan(req Request) *Plan {
	return &Plan{
		Request:  req,
		Lines:    []LinePlan{},
		Phases:   []TokenPhase{},
		Skipped:  []SkipRow{},
		Total:    big.NewInt(0),
		Gas:      big.NewInt(0),
		Warnings: []string{},
	}
}

func (f *Flow) newPhase(item TokenLine) TokenPhase {
	return TokenPhase{Token: item, Fee: big.NewInt(0)}
}

func (f *Flow) lineAppender(plan *Plan) func(LinePlan) {
	return func(line LinePlan) {
		if line.Fee == nil {
			line.Fee = big.NewInt(0)
		}
		if line.Value == nil {
			line.Value = big.NewInt(0)
		}
		line.Nonce = uint64(len(plan.Lines))
		plan.Lines = append(plan.Lines, line)
		plan.Gas.Add(plan.Gas, line.Fee)
	}
}

func (f *Flow) planGas(plan *Plan) *big.Int {
	return new(big.Int).Set(plan.Gas)
}

func (f *Flow) totalValue(plan *Plan) *big.Int {
	total := big.NewInt(0)
	for _, line := range plan.Lines {
		total.Add(total, line.Value)
	}
	return total
}

func (f *Flow) nativeItemOf(item TokenLine) TokenLine {
	item.Address = ""
	item.Native = true
	item.Decimals = f.NativeDecimals()
	if item.Symbol == "" {
		item.Symbol = f.NativeSymbol()
	}
	return item
}

func (f *Flow) transferLine(owner wallet.PublicWallet, to string, item TokenLine, amount *big.Int) LinePlan {
	if item.Native {
		return LinePlan{Kind: KindTransfer, Owner: owner, Token: item, Recipient: to, To: to, Value: amount}
	}
	return LinePlan{Kind: KindTransfer, Owner: owner, Token: item, Recipient: to, To: item.Address, Value: amount}
}
