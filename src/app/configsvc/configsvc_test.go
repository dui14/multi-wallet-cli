package configsvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"multi-wallet-cli/domain/errcodes"
	"multi-wallet-cli/infra/config"
	"multi-wallet-cli/infra/keystore"
)

type stubRepo struct{}

func (stubRepo) SaveAgentConfig(context.Context, keystore.SaveAgentConfigRequest) error { return nil }
func (stubRepo) DeleteAgentConfig(context.Context) error                                { return nil }
func (stubRepo) SaveLanguage(context.Context, string) error                             { return nil }

const testNetworks = `{
  "_metadata": { "verifiedAt": "2026-10-07" },
  "ethereum": { "name": "Ethereum Mainnet", "vmType": "evm", "chainId": 1, "relayChainId": 1, "nativeSymbol": "ETH", "nativeDecimals": 18, "seedRpcs": ["https://ethereum-rpc.publicnode.com"] }
}`

const testTokens = `{
  "_metadata": { "verifiedAt": "2026-10-07" },
  "ethereum": { "USDC": { "address": "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", "decimals": 6, "symbol": "USDC" } }
}`

func newTestService(t *testing.T) *Service {
	t.Helper()
	baseDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseDir, "networks.json"), []byte(testNetworks), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "tokens.json"), []byte(testTokens), 0o600); err != nil {
		t.Fatal(err)
	}
	return &Service{repo: stubRepo{}, baseDir: baseDir}
}

func TestAddNetwork_EVMSuccess(t *testing.T) {
	service := newTestService(t)
	slug, err := service.AddNetwork(context.Background(), NetworkDraft{
		Name:           "Linea Mainnet",
		ChainID:        59144,
		VMType:         "EVM",
		NativeSymbol:   "eth",
		NativeDecimals: 18,
		RPCs:           []string{"https://rpc.linea.build", "https://rpc.linea.build"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if slug != "linea-mainnet" {
		t.Fatalf("slug = %q, want linea-mainnet", slug)
	}
}

func TestAddNetwork_SVMAutoFill(t *testing.T) {
	service := newTestService(t)
	slug, err := service.AddNetwork(context.Background(), NetworkDraft{
		Name:           "Eclipse",
		VMType:         "svm",
		NativeSymbol:   "ETH",
		NativeDecimals: 9,
		RPCs:           []string{"https://eclipse.example/rpc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if slug != "eclipse" {
		t.Fatalf("slug = %q, want eclipse", slug)
	}
}

func TestAddNetwork_RejectsInvalid(t *testing.T) {
	service := newTestService(t)
	cases := []NetworkDraft{
		{Name: "", VMType: "evm", NativeSymbol: "ETH", NativeDecimals: 18, RPCs: []string{"https://a.example"}},
		{Name: "Bad VM", VMType: "wasm", NativeSymbol: "ETH", NativeDecimals: 18, RPCs: []string{"https://a.example"}},
		{Name: "No Symbol", VMType: "evm", NativeSymbol: "", NativeDecimals: 18, RPCs: []string{"https://a.example"}},
		{Name: "Bad Decimals", VMType: "evm", NativeSymbol: "ETH", NativeDecimals: 99, RPCs: []string{"https://a.example"}},
		{Name: "Bad RPC", VMType: "evm", NativeSymbol: "ETH", NativeDecimals: 18, RPCs: []string{"not-a-url"}},
		{Name: "Ethereum", VMType: "evm", ChainID: 1, NativeSymbol: "ETH", NativeDecimals: 18, RPCs: []string{"https://a.example"}},
	}
	for _, draft := range cases {
		if _, err := service.AddNetwork(context.Background(), draft); !errors.Is(err, errcodes.ErrInvalidInput) {
			t.Fatalf("draft %+v: expected ErrInvalidInput, got %v", draft, err)
		}
	}
}

func TestAddNetwork_SanitizesRPCScheme(t *testing.T) {
	service := newTestService(t)
	_, err := service.AddNetwork(context.Background(), NetworkDraft{
		Name:           "FTP Chain",
		VMType:         "evm",
		ChainID:        999,
		NativeSymbol:   "ETH",
		NativeDecimals: 18,
		RPCs:           []string{"ftp://rpc.example"},
	})
	if !errors.Is(err, errcodes.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for non-http scheme, got %v", err)
	}
}

func TestResolveAndSaveToken(t *testing.T) {
	service := newTestService(t)
	resolver := func(context.Context, string, string) (config.Token, error) {
		return config.Token{Symbol: "drv", Decimals: 18}, nil
	}
	token, err := service.ResolveToken(context.Background(), "ethereum", "0x503828976D22510aad0201ac7EC88293211D23Da", resolver)
	if err != nil {
		t.Fatal(err)
	}
	if token.Symbol != "DRV" || token.Decimals != 18 {
		t.Fatalf("token = %#v", token)
	}
	if err := service.SaveToken("ethereum", token); err != nil {
		t.Fatal(err)
	}
}

func TestResolveToken_RejectsBadAddress(t *testing.T) {
	service := newTestService(t)
	resolver := func(context.Context, string, string) (config.Token, error) {
		t.Fatal("resolver must not be called for an invalid address")
		return config.Token{}, nil
	}
	if _, err := service.ResolveToken(context.Background(), "ethereum", "0xnothex", resolver); !errors.Is(err, errcodes.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Linea Mainnet": "linea-mainnet",
		"  BNB  Chain ": "bnb-chain",
		"Polygon_z":     "polygon-z",
		"...":           "",
		"Base Goerli":   "base-goerli",
	}
	for input, want := range cases {
		if got := slugify(input); got != want {
			t.Fatalf("slugify(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestExplorerForChainID(t *testing.T) {
	cases := map[int64]string{
		1:        "https://etherscan.io/",
		56:       "https://bscscan.com/",
		137:      "https://polygonscan.com/",
		11155111: "https://sepolia.etherscan.io/",
	}
	for chainID, want := range cases {
		if got := ExplorerForChainID(chainID); got != want {
			t.Fatalf("ExplorerForChainID(%d) = %q, want %q", chainID, got, want)
		}
	}
	if got := ExplorerForChainID(999999); got != "" {
		t.Fatalf("ExplorerForChainID(unknown) = %q, want empty", got)
	}
}

func TestResolveNetworkExplorer_UsesLiveIdentity(t *testing.T) {
	network := config.Network{}
	if err := resolveNetworkExplorer(&network, "evm", "56", 0); err != nil {
		t.Fatal(err)
	}
	if network.Explorer != "https://bscscan.com/" {
		t.Fatalf("explorer = %q, want bscscan", network.Explorer)
	}
}

func TestResolveNetworkExplorer_FallsBackToDeclaredChainID(t *testing.T) {
	network := config.Network{}
	if err := resolveNetworkExplorer(&network, "evm", "", 137); err != nil {
		t.Fatal(err)
	}
	if network.Explorer != "https://polygonscan.com/" {
		t.Fatalf("explorer = %q, want polygonscan", network.Explorer)
	}
}

func TestResolveNetworkExplorer_RequiresKnownExplorer(t *testing.T) {
	network := config.Network{}
	if err := resolveNetworkExplorer(&network, "evm", "999999", 0); !errors.Is(err, errcodes.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for unknown chain, got %v", err)
	}
}

func TestResolveNetworkExplorer_KeepsExistingExplorer(t *testing.T) {
	network := config.Network{Explorer: "https://custom.example/"}
	if err := resolveNetworkExplorer(&network, "evm", "56", 0); err != nil {
		t.Fatal(err)
	}
	if network.Explorer != "https://custom.example/" {
		t.Fatalf("explorer = %q, want the existing value kept", network.Explorer)
	}
}

func TestResolveNetworkExplorer_SkipsNonEVM(t *testing.T) {
	network := config.Network{}
	if err := resolveNetworkExplorer(&network, "svm", "", 0); err != nil {
		t.Fatal(err)
	}
	if network.Explorer != "" {
		t.Fatalf("explorer = %q, want empty for non-EVM", network.Explorer)
	}
}

func TestAddNetwork_EVMPersistsResolvedExplorer(t *testing.T) {
	service := newTestService(t)
	if _, err := service.AddNetwork(context.Background(), NetworkDraft{
		Name:           "Polygon",
		ChainID:        137,
		VMType:         "evm",
		NativeSymbol:   "POL",
		NativeDecimals: 18,
		RPCs:           []string{"https://polygon-rpc.example"},
	}); err != nil {
		t.Fatal(err)
	}
	networks, err := config.LoadNetworks(service.baseDir)
	if err != nil {
		t.Fatal(err)
	}
	if networks["polygon"].Explorer != "https://polygonscan.com/" {
		t.Fatalf("persisted explorer = %q, want polygonscan", networks["polygon"].Explorer)
	}
}
