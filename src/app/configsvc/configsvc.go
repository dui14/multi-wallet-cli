package configsvc

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"

	"multi-wallet-cli/domain/errcodes"
	"multi-wallet-cli/infra/config"
	"multi-wallet-cli/infra/crypto"
	"multi-wallet-cli/infra/i18n"
	"multi-wallet-cli/infra/keystore"
)

const (
	solanaRelayChainID = 792703809
	defaultSVMCluster  = "mainnet-beta"
	maxTokenDecimals   = 36
)

var supportedVMTypes = map[string]bool{"evm": true, "svm": true, "tvm": true, "move": true}

type Repository interface {
	SaveAgentConfig(ctx context.Context, req keystore.SaveAgentConfigRequest) error
	DeleteAgentConfig(ctx context.Context) error
	SaveLanguage(ctx context.Context, language string) error
}

type Service struct {
	repo       Repository
	baseDir    string
	validators ValidatorRegistry
}

// NetworkDraft carries the user-provided fields for a custom network.
type NetworkDraft struct {
	Name           string
	ChainID        int64
	VMType         string
	NativeSymbol   string
	NativeDecimals uint8
	RPCs           []string
}

// TokenResolver reads a token's on-chain metadata for a chain. It is injected
// so the service layer stays free of RPC client wiring.
type TokenResolver func(ctx context.Context, chain string, address string) (config.Token, error)

var providerModels = map[string]string{
	"gemini":     "gemini-1.5-flash",
	"openai":     "gpt-4o-mini",
	"claude":     "claude-3-5-sonnet-latest",
	"openrouter": "qwen/qwen-2.5-coder-7b",
}

func New(repo Repository, baseDir string) *Service {
	return &Service{repo: repo, baseDir: baseDir, validators: DefaultValidators()}
}

func (s *Service) SetLanguage(ctx context.Context, language string) error {
	language = strings.ToLower(strings.TrimSpace(language))
	if language != "en" && language != "vi" {
		return fmt.Errorf("%w: language must be en or vi", errcodes.ErrInvalidInput)
	}
	if err := s.repo.SaveLanguage(ctx, language); err != nil {
		return fmt.Errorf("save language: %w", err)
	}
	i18n.SetLanguage(language)
	return nil
}

func (s *Service) SetAgentProvider(ctx context.Context, provider string, apiKey []byte, password []byte) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	model, ok := providerModels[provider]
	if !ok {
		return fmt.Errorf("%w: unsupported provider", errcodes.ErrInvalidInput)
	}
	trimmed := []byte(strings.TrimSpace(string(apiKey)))
	defer crypto.Zeroize(trimmed)
	if len(trimmed) == 0 {
		return fmt.Errorf("%w: api key is required", errcodes.ErrInvalidInput)
	}
	if len(password) == 0 {
		return fmt.Errorf("%w: master password is required", errcodes.ErrInvalidInput)
	}
	if err := s.repo.SaveAgentConfig(ctx, keystore.SaveAgentConfigRequest{
		Provider: provider,
		Model:    model,
		APIKey:   trimmed,
		Password: password,
	}); err != nil {
		return fmt.Errorf("save agent configuration: %w", err)
	}
	config.CurrentAgentConfig = config.AgentConfig{
		Provider: provider,
		Model:    model,
		APIKey:   string(trimmed),
	}
	return nil
}

func (s *Service) DeleteAgentProvider(ctx context.Context) error {
	if err := s.repo.DeleteAgentConfig(ctx); err != nil {
		return fmt.Errorf("delete agent configuration: %w", err)
	}
	config.CurrentAgentConfig = config.AgentConfig{}
	return nil
}

func ProviderModels() map[string]string {
	models := make(map[string]string, len(providerModels))
	for provider, model := range providerModels {
		models[provider] = model
	}
	return models
}

// AddNetwork validates a custom network draft, derives the slug and any missing
// chain fields, then persists it into networks.json.
func (s *Service) AddNetwork(ctx context.Context, draft NetworkDraft) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name := strings.TrimSpace(draft.Name)
	if name == "" {
		return "", fmt.Errorf("%w: network name is required", errcodes.ErrInvalidInput)
	}
	slug := slugify(name)
	if slug == "" {
		return "", fmt.Errorf("%w: network name must contain letters or digits", errcodes.ErrInvalidInput)
	}
	vmType := strings.ToLower(strings.TrimSpace(draft.VMType))
	if !supportedVMTypes[vmType] {
		return "", fmt.Errorf("%w: vmType must be evm, svm, tvm, or move", errcodes.ErrInvalidInput)
	}
	if draft.NativeDecimals > maxTokenDecimals {
		return "", fmt.Errorf("%w: native decimals must be 0-%d", errcodes.ErrInvalidInput, maxTokenDecimals)
	}
	symbol := strings.ToUpper(strings.TrimSpace(draft.NativeSymbol))
	if symbol == "" {
		return "", fmt.Errorf("%w: native symbol is required", errcodes.ErrInvalidInput)
	}
	rpcs, err := sanitizeRPCs(draft.RPCs)
	if err != nil {
		return "", err
	}
	networks, err := config.LoadNetworks(s.baseDir)
	if err != nil {
		return "", err
	}
	if _, exists := networks[slug]; exists {
		return "", fmt.Errorf("%w: network %q already exists", errcodes.ErrInvalidInput, slug)
	}
	network := buildNetwork(draft, slug, name, vmType, symbol, rpcs)
	identity, err := s.validateNetwork(ctx, networks, slug, vmType, draft, network, rpcs)
	if err != nil {
		return "", err
	}
	if err := resolveNetworkExplorer(&network, vmType, identity, draft.ChainID); err != nil {
		return "", err
	}
	if err := config.SaveNetwork(s.baseDir, slug, network); err != nil {
		return "", err
	}
	return slug, nil
}

// resolveNetworkExplorer fills network.Explorer from the chain id reported live
// by eth_chainId (falling back to the declared chain id) so receipts and config
// summaries can link to a working explorer. EVM networks must resolve to at
// least one known explorer; non-EVM networks keep any explorer already present.
func resolveNetworkExplorer(network *config.Network, vmType, identity string, declaredChainID int64) error {
	if vmType != "evm" {
		return nil
	}
	chainID := declaredChainID
	if strings.TrimSpace(identity) != "" {
		if parsed, parseErr := strconv.ParseInt(strings.TrimSpace(identity), 10, 64); parseErr == nil {
			chainID = parsed
		}
	}
	if network.Explorer == "" {
		if chainID == 0 {
			return fmt.Errorf("%w: chain id required to resolve a block explorer", errcodes.ErrInvalidInput)
		}
		explorer := ExplorerForChainID(chainID)
		if explorer == "" {
			return fmt.Errorf("%w: no supported block explorer for chain id %d", errcodes.ErrInvalidInput, chainID)
		}
		network.Explorer = explorer
	}
	return nil
}

// DeleteNetwork removes a user-added network from networks.json.
func (s *Service) DeleteNetwork(ctx context.Context, slug string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	trimmed := strings.ToLower(strings.TrimSpace(slug))
	if trimmed == "" || strings.HasPrefix(trimmed, "_") {
		return fmt.Errorf("%w: invalid network slug", errcodes.ErrInvalidInput)
	}
	networks, err := config.LoadNetworks(s.baseDir)
	if err != nil {
		return err
	}
	if _, exists := networks[trimmed]; !exists {
		return fmt.Errorf("%w: network %q not found", errcodes.ErrInvalidInput, trimmed)
	}
	if err := config.DeleteNetwork(s.baseDir, trimmed); err != nil {
		return err
	}
	return nil
}

// DeleteToken removes a token from tokens.json under the given chain.
func (s *Service) DeleteToken(ctx context.Context, chain string, symbol string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	trimmedChain := strings.ToLower(strings.TrimSpace(chain))
	trimmedSymbol := strings.ToUpper(strings.TrimSpace(symbol))
	if trimmedChain == "" || trimmedSymbol == "" {
		return fmt.Errorf("%w: chain and symbol are required", errcodes.ErrInvalidInput)
	}
	tokens, err := config.LoadTokens(s.baseDir)
	if err != nil {
		return err
	}
	if _, exists := tokens[trimmedChain][trimmedSymbol]; !exists {
		return fmt.Errorf("%w: token %q not found on %s", errcodes.ErrInvalidInput, trimmedSymbol, trimmedChain)
	}
	if err := config.DeleteToken(s.baseDir, trimmedChain, trimmedSymbol); err != nil {
		return err
	}
	return nil
}

// buildNetwork assembles a config.Network from a validated draft.
func buildNetwork(draft NetworkDraft, slug, name, vmType, symbol string, rpcs []string) config.Network {
	network := config.Network{
		Name:           name,
		VMType:         vmType,
		NativeSymbol:   symbol,
		NativeDecimals: draft.NativeDecimals,
		SeedRPCs:       rpcs,
	}
	switch vmType {
	case "evm":
		network.ChainID = draft.ChainID
		network.RelayChainID = draft.ChainID
		network.EIP1559 = true
	case "svm":
		network.Cluster = defaultSVMCluster
		network.RelayChainID = solanaRelayChainID
	default:
		network.ChainID = draft.ChainID
		network.RelayChainID = draft.ChainID
	}
	return network
}

// ResolveToken validates a contract address and reads its on-chain symbol and
// decimals without persisting anything.
func (s *Service) ResolveToken(ctx context.Context, chain string, address string, resolver TokenResolver) (config.Token, error) {
	if err := ctx.Err(); err != nil {
		return config.Token{}, err
	}
	trimmedChain := strings.ToLower(strings.TrimSpace(chain))
	if trimmedChain == "" {
		return config.Token{}, fmt.Errorf("%w: chain is required", errcodes.ErrInvalidInput)
	}
	normalized := strings.TrimSpace(address)
	if !common.IsHexAddress(normalized) {
		return config.Token{}, fmt.Errorf("%w: token address is not a valid contract address", errcodes.ErrInvalidInput)
	}
	normalized = common.HexToAddress(normalized).Hex()
	if resolver == nil {
		return config.Token{}, fmt.Errorf("%w: token resolver is required", errcodes.ErrInvalidInput)
	}
	resolved, err := resolver(ctx, trimmedChain, normalized)
	if err != nil {
		return config.Token{}, err
	}
	resolved.Address = normalized
	resolved.Symbol = strings.ToUpper(strings.TrimSpace(resolved.Symbol))
	if resolved.Symbol == "" {
		return config.Token{}, fmt.Errorf("%w: token symbol could not be resolved", errcodes.ErrInvalidInput)
	}
	if resolved.Decimals > maxTokenDecimals {
		return config.Token{}, fmt.Errorf("%w: token decimals out of range", errcodes.ErrInvalidInput)
	}
	return resolved, nil
}

// SaveToken persists an already-resolved token into tokens.json under chain.
func (s *Service) SaveToken(chain string, token config.Token) error {
	trimmedChain := strings.ToLower(strings.TrimSpace(chain))
	if trimmedChain == "" {
		return fmt.Errorf("%w: chain is required", errcodes.ErrInvalidInput)
	}
	return config.SaveToken(s.baseDir, trimmedChain, token.Symbol, token)
}

func sanitizeRPCs(rpcs []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(rpcs))
	for _, raw := range rpcs {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		parsed, err := url.Parse(trimmed)
		if err != nil || parsed.Host == "" || !isSupportedRPCScheme(parsed.Scheme) {
			return nil, fmt.Errorf("%w: invalid RPC URL %q", errcodes.ErrInvalidInput, trimmed)
		}
		if seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	return out, nil
}

func isSupportedRPCScheme(scheme string) bool {
	switch scheme {
	case "http", "https", "ws", "wss":
		return true
	default:
		return false
	}
}

func slugify(value string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case r == ' ' || r == '-' || r == '_' || r == '.':
			if !lastDash && builder.Len() > 0 {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(builder.String(), "-")
}
