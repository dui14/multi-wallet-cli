package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

type AgentConfig struct {
	Provider string
	Model    string
	APIKey   string
}

var CurrentAgentConfig AgentConfig

type Contracts struct {
	WrappedNative           string `json:"wrappedNative"`
	EIP7702Executor         string `json:"eip7702Executor,omitempty"`
	EIP7702ExecutorCodeHash string `json:"eip7702ExecutorCodeHash,omitempty"`
}

// TransferSettings holds owner-batch thresholds from networks.json _metadata.transfer.
type TransferSettings struct {
	EIP7702MinCalls              int `json:"eip7702MinCalls"`
	EIP7702MinCallsWhenDelegated int `json:"eip7702MinCallsWhenDelegated"`
}

type Network struct {
	Name           string    `json:"name"`
	VMType         string    `json:"vmType"`
	ChainID        int64     `json:"chainId"`
	Cluster        string    `json:"cluster"`
	RelayChainID   int64     `json:"relayChainId"`
	RPC            string    `json:"rpc,omitempty"`
	SeedRPCs       []string  `json:"seedRpcs"`
	NativeSymbol   string    `json:"nativeSymbol"`
	NativeDecimals uint8     `json:"nativeDecimals"`
	EIP1559        bool      `json:"eip1559"`
	Explorer       string    `json:"explorer"`
	Contracts      Contracts `json:"contracts"`
}

// PrimaryRPC returns the first endpoint usable by the JSON-RPC HTTP transport,
// falling back to the first declared endpoint so web-socket-only networks still
// report their configured URL.
func (n Network) PrimaryRPC() string {
	for _, endpoint := range n.SeedRPCs {
		if isHTTPRPCURL(endpoint) {
			return strings.TrimSpace(endpoint)
		}
	}
	if len(n.SeedRPCs) > 0 {
		return strings.TrimSpace(n.SeedRPCs[0])
	}
	return n.RPC
}

func isHTTPRPCURL(rawURL string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(rawURL))
	return strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://")
}

func (n Network) SeedEndpoints() []string {
	if len(n.SeedRPCs) > 0 {
		out := make([]string, len(n.SeedRPCs))
		copy(out, n.SeedRPCs)
		return out
	}
	if n.RPC != "" {
		return []string{n.RPC}
	}
	return nil
}

type Token struct {
	Address  string `json:"address"`
	Decimals uint8  `json:"decimals"`
	Symbol   string `json:"symbol"`
	Name     string `json:"name,omitempty"`
}

func LoadNetworks(baseDir string) (map[string]Network, error) {
	data, err := os.ReadFile(filepath.Join(baseDir, "networks.json"))
	if err != nil {
		return nil, fmt.Errorf("read networks: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode networks: %w", err)
	}
	delete(raw, "_metadata")
	networks := make(map[string]Network, len(raw))
	for slug, rawNetwork := range raw {
		var network Network
		if err := json.Unmarshal(rawNetwork, &network); err != nil {
			return nil, fmt.Errorf("decode networks for %s: %w", slug, err)
		}
		if network.RPC == "" && len(network.SeedRPCs) > 0 {
			network.RPC = network.SeedRPCs[0]
		}
		if len(network.SeedRPCs) == 0 && network.RPC != "" {
			network.SeedRPCs = []string{network.RPC}
		}
		networks[slug] = network
	}
	return networks, nil
}

// LoadTransferSettings reads _metadata.transfer. Both thresholds must be present.
func LoadTransferSettings(baseDir string) (TransferSettings, error) {
	data, err := os.ReadFile(filepath.Join(baseDir, "networks.json"))
	if err != nil {
		return TransferSettings{}, fmt.Errorf("read networks: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return TransferSettings{}, fmt.Errorf("decode networks: %w", err)
	}
	metaRaw, ok := raw["_metadata"]
	if !ok {
		return TransferSettings{}, fmt.Errorf("networks missing transfer thresholds")
	}
	var meta struct {
		Transfer TransferSettings `json:"transfer"`
	}
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return TransferSettings{}, fmt.Errorf("decode transfer thresholds: %w", err)
	}
	if meta.Transfer.EIP7702MinCalls <= 0 || meta.Transfer.EIP7702MinCallsWhenDelegated <= 0 {
		return TransferSettings{}, fmt.Errorf("networks missing transfer thresholds")
	}
	return meta.Transfer, nil
}

func LoadTokens(baseDir string) (map[string]map[string]Token, error) {
	data, err := os.ReadFile(filepath.Join(baseDir, "tokens.json"))
	if err != nil {
		return nil, fmt.Errorf("read tokens: %w", err)
	}
	var rawTokens map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawTokens); err != nil {
		return nil, fmt.Errorf("decode tokens: %w", err)
	}
	delete(rawTokens, "_warning")
	delete(rawTokens, "_metadata")
	tokens := make(map[string]map[string]Token, len(rawTokens))
	for chain, rawChainTokens := range rawTokens {
		var decoded map[string]json.RawMessage
		if err := json.Unmarshal(rawChainTokens, &decoded); err != nil {
			return nil, fmt.Errorf("decode tokens for %s: %w", chain, err)
		}
		chainTokens := make(map[string]Token, len(decoded))
		for symbol, rawToken := range decoded {
			var legacy string
			if err := json.Unmarshal(rawToken, &legacy); err == nil {
				chainTokens[symbol] = Token{Address: legacy, Symbol: symbol}
				continue
			}
			var token Token
			if err := json.Unmarshal(rawToken, &token); err != nil {
				return nil, fmt.Errorf("decode token %s for %s: %w", symbol, chain, err)
			}
			if token.Symbol == "" {
				token.Symbol = symbol
			}
			chainTokens[symbol] = token
		}
		tokens[chain] = chainTokens
	}
	return tokens, nil
}

func TokenAddress(tokens map[string]map[string]Token, chain string, symbol string) string {
	return tokens[chain][symbol].Address
}

func IsEVMChecksumAddress(address string) bool {
	if !common.IsHexAddress(address) {
		return false
	}
	return strings.HasPrefix(address, "0x") && common.HexToAddress(address).Hex() == address
}

// SaveNetwork inserts or updates one network under slug in networks.json while
// preserving the _metadata block and every other entry. The write is atomic.
func SaveNetwork(baseDir string, slug string, network Network) error {
	path := filepath.Join(baseDir, "networks.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read networks: %w", err)
	}
	document := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &document); err != nil {
		return fmt.Errorf("decode networks: %w", err)
	}
	encoded, err := json.Marshal(network)
	if err != nil {
		return fmt.Errorf("encode network %s: %w", slug, err)
	}
	document[slug] = encoded
	return writeRegistryFile(path, document)
}

// DeleteNetwork removes the network under slug from networks.json while
// preserving the _metadata block and every other entry. The write is atomic.
func DeleteNetwork(baseDir string, slug string) error {
	path := filepath.Join(baseDir, "networks.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read networks: %w", err)
	}
	document := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &document); err != nil {
		return fmt.Errorf("decode networks: %w", err)
	}
	if _, ok := document[slug]; !ok {
		return fmt.Errorf("network %q not found", slug)
	}
	delete(document, slug)
	return writeRegistryFile(path, document)
}

// DeleteToken removes the token under symbol for chain in tokens.json while
// preserving the _metadata block and every other chain. The write is atomic.
func DeleteToken(baseDir string, chain string, symbol string) error {
	path := filepath.Join(baseDir, "tokens.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read tokens: %w", err)
	}
	document := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &document); err != nil {
		return fmt.Errorf("decode tokens: %w", err)
	}
	chainTokens := map[string]json.RawMessage{}
	existing, ok := document[chain]
	if !ok {
		return fmt.Errorf("chain %q not found", chain)
	}
	if err := json.Unmarshal(existing, &chainTokens); err != nil {
		return fmt.Errorf("decode tokens for %s: %w", chain, err)
	}
	if _, ok := chainTokens[symbol]; !ok {
		return fmt.Errorf("token %q not found on %s", symbol, chain)
	}
	delete(chainTokens, symbol)
	chainRaw, err := json.Marshal(chainTokens)
	if err != nil {
		return fmt.Errorf("encode tokens for %s: %w", chain, err)
	}
	document[chain] = chainRaw
	return writeRegistryFile(path, document)
}

// SaveToken inserts or updates one token under symbol for chain in tokens.json
// while preserving the _metadata block and every other chain. The write is atomic.
func SaveToken(baseDir string, chain string, symbol string, token Token) error {
	path := filepath.Join(baseDir, "tokens.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read tokens: %w", err)
	}
	document := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &document); err != nil {
		return fmt.Errorf("decode tokens: %w", err)
	}
	chainTokens := map[string]json.RawMessage{}
	if existing, ok := document[chain]; ok {
		if err := json.Unmarshal(existing, &chainTokens); err != nil {
			return fmt.Errorf("decode tokens for %s: %w", chain, err)
		}
	}
	encoded, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("encode token %s: %w", symbol, err)
	}
	chainTokens[symbol] = encoded
	chainRaw, err := json.Marshal(chainTokens)
	if err != nil {
		return fmt.Errorf("encode tokens for %s: %w", chain, err)
	}
	document[chain] = chainRaw
	return writeRegistryFile(path, document)
}

func writeRegistryFile(path string, document map[string]json.RawMessage) error {
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode registry: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".registry-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("commit registry: %w", err)
	}
	cleanup = false
	return nil
}
