package balance

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"golang.org/x/sync/errgroup"

	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/config"
	"multi-wallet-cli/infra/evm"
	"multi-wallet-cli/infra/rpc"
	"multi-wallet-cli/infra/solana"
)

type Request struct {
	Wallets []wallet.PublicWallet
	Chains  []string
	Tokens  map[string][]string
	Native  bool
	Custom  map[string]CustomToken
}

// CustomToken carries a user-supplied contract whose metadata was already read
// from the chain RPC (name, symbol and decimals) so the scan only batches the
// balanceOf call and can render the resolved symbol as a column.
type CustomToken struct {
	Symbol   string
	Name     string
	Address  string
	Decimals uint8
}

// Label returns the column name for the custom token, preferring the on-chain
// symbol, then the name, then a generic fallback when neither is exposed.
func (c CustomToken) Label() string {
	if symbol := strings.TrimSpace(c.Symbol); symbol != "" {
		return symbol
	}
	if name := strings.TrimSpace(c.Name); name != "" {
		return name
	}
	return FallbackTokenSymbol
}

// RowErrorToken marks a row that reports a chain-level failure instead of one
// token amount.
const RowErrorToken = "RPC"

// FallbackTokenSymbol names a contract that exposes neither symbol nor name.
const FallbackTokenSymbol = "CUSTOM"

type Row struct {
	Wallet, Address, Chain, Token string
	Native                        bool
	Raw                           *big.Int
	Decimals                      uint8
	Err                           error
}
type Report struct{ Rows []Row }
type Service struct {
	mu       sync.RWMutex
	networks map[string]config.Network
	tokens   map[string]map[string]config.Token
	pools    map[string]*rpc.Pool
	evm      *evm.Client
	solana   *solana.Client
}

func New(baseDir string) (*Service, error) {
	networks, err := config.LoadNetworks(baseDir)
	if err != nil {
		return nil, err
	}
	tokens, err := config.LoadTokens(baseDir)
	if err != nil {
		return nil, err
	}
	return &Service{networks: networks, tokens: tokens, pools: buildPools(networks), evm: evm.NewClient(), solana: solana.NewClient()}, nil
}

// buildPools creates one seed-tier rotation pool per network so a failing
// endpoint rotates to the remaining ones instead of aborting the scan.
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
	s.mu.Lock()
	s.networks = networks
	s.tokens = tokens
	s.pools = buildPools(networks)
	s.mu.Unlock()
	return nil
}

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

func (s *Service) Scan(ctx context.Context, request Request) (Report, error) {
	if len(request.Wallets) == 0 || len(request.Chains) == 0 {
		return Report{}, fmt.Errorf("select at least one wallet and chain")
	}
	hasToken := false
	for _, tokens := range request.Tokens {
		if len(tokens) > 0 {
			hasToken = true
			break
		}
	}
	hasCustom := false
	for _, custom := range request.Custom {
		if strings.TrimSpace(custom.Address) != "" {
			hasCustom = true
			break
		}
	}
	if !request.Native && !hasToken && !hasCustom {
		return Report{}, fmt.Errorf("select at least one token or enter a contract")
	}
	s.mu.RLock()
	networks := s.networks
	tokens := s.tokens
	s.mu.RUnlock()
	jobs := make([]scanJob, 0)
	for _, item := range request.Wallets {
		for _, chainName := range request.Chains {
			network, ok := networks[chainName]
			if !ok {
				continue
			}
			if (chainName == "solana") != (item.ChainKind == wallet.ChainKindSolana) {
				continue
			}
			jobs = append(jobs, scanJob{wallet: item, chain: chainName, network: network, tokens: tokens[chainName]})
		}
	}
	rows := make([]Row, 0)
	var mu sync.Mutex
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(4)
	for _, job := range jobs {
		job := job
		group.Go(func() error {
			jobRows := s.scanJob(groupCtx, request, job)
			mu.Lock()
			rows = append(rows, jobRows...)
			mu.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return Report{}, err
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Wallet+rows[i].Chain+rows[i].Token < rows[j].Wallet+rows[j].Chain+rows[j].Token
	})
	return Report{Rows: rows}, nil
}

type scanJob struct {
	wallet  wallet.PublicWallet
	chain   string
	network config.Network
	tokens  map[string]config.Token
}

func (s *Service) scanJob(ctx context.Context, request Request, job scanJob) []Row {
	if job.chain == "solana" {
		if !request.Native {
			return nil
		}
		pool := s.pool(job.chain)
		var result solana.Result
		var err error
		if pool == nil {
			result, err = s.solana.Scan(ctx, job.network.PrimaryRPC(), job.wallet.Address, job.network.NativeSymbol)
		} else {
			err = pool.Do(ctx, func(callCtx context.Context, endpoint string) error {
				var scanErr error
				result, scanErr = s.solana.Scan(callCtx, endpoint, job.wallet.Address, job.network.NativeSymbol)
				return scanErr
			})
		}
		row := Row{Wallet: job.wallet.Label, Address: job.wallet.Address, Chain: job.chain, Token: job.network.NativeSymbol, Native: true, Decimals: job.network.NativeDecimals, Err: err}
		if err == nil {
			row.Raw, row.Decimals = result.Raw, result.Decimals
		}
		return []Row{row}
	}
	selected := request.Tokens[job.chain]
	known := make([]evm.Token, 0, len(selected)+1)
	seen := map[string]bool{}
	for _, symbol := range selected {
		if token, ok := job.tokens[symbol]; ok && token.Address != "" {
			address := common.HexToAddress(token.Address).Hex()
			if seen[address] {
				continue
			}
			seen[address] = true
			known = append(known, evm.Token{Symbol: symbol, Address: address, Decimals: token.Decimals})
		}
	}
	if custom, ok := request.Custom[job.chain]; ok && strings.TrimSpace(custom.Address) != "" {
		address := common.HexToAddress(custom.Address).Hex()
		if !seen[address] {
			seen[address] = true
			known = append(known, evm.Token{Symbol: custom.Label(), Address: address, Decimals: custom.Decimals})
		}
	}
	pool := s.pool(job.chain)
	var results []evm.Result
	var err error
	if pool == nil {
		results, err = s.evm.Scan(ctx, job.network.PrimaryRPC(), job.wallet.Address, job.network.NativeSymbol, job.network.NativeDecimals, known, request.Native)
	} else {
		err = pool.Do(ctx, func(callCtx context.Context, endpoint string) error {
			var scanErr error
			results, scanErr = s.evm.Scan(callCtx, endpoint, job.wallet.Address, job.network.NativeSymbol, job.network.NativeDecimals, known, request.Native)
			return scanErr
		})
	}
	if err != nil {
		return []Row{{Wallet: job.wallet.Label, Address: job.wallet.Address, Chain: job.chain, Token: RowErrorToken, Err: err}}
	}
	rows := make([]Row, 0, len(results))
	for _, result := range results {
		rows = append(rows, Row{
			Wallet:   job.wallet.Label,
			Address:  job.wallet.Address,
			Chain:    job.chain,
			Token:    result.Symbol,
			Native:   result.Native,
			Raw:      result.Raw,
			Decimals: result.Decimals,
			Err:      result.Err,
		})
	}
	return rows
}

func FormatAmount(raw *big.Int, decimals uint8) string {
	if raw == nil {
		return "-"
	}
	divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	value := new(big.Float).Quo(new(big.Float).SetInt(raw), new(big.Float).SetInt(divisor))
	return value.Text('f', 6)
}
