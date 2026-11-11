package cli

import (
	"sync"

	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/config"
)

// keySignerHolder lazily resolves wallet signing keys once the set is unlocked.
// The Relay execution engine is built before unlock, so it captures this holder
// and reads the real signer only at execution time.
type keySignerHolder struct {
	mu     sync.RWMutex
	signer func(label string) ([]byte, bool)
}

func (h *keySignerHolder) set(unlocked *wallet.WalletSet) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.signer = relayKeySigner(unlocked)
}

func (h *keySignerHolder) sign(label string) ([]byte, bool) {
	h.mu.RLock()
	current := h.signer
	h.mu.RUnlock()
	if current == nil {
		return nil, false
	}
	return current(label)
}

// relayKeySigner copies a wallet's private key out of RAM at signing time,
// mirroring the transfer signer so keys are never retained in the holder.
func relayKeySigner(unlocked *wallet.WalletSet) func(label string) ([]byte, bool) {
	return func(label string) ([]byte, bool) {
		if unlocked == nil {
			return nil, false
		}
		entry := unlocked.ByLabel(label)
		if entry == nil {
			return nil, false
		}
		key := entry.PrivateKeyCopy()
		if len(key) == 0 {
			return nil, false
		}
		return key, true
	}
}

// loadSwapRegistries reads networks.json and tokens.json for the swap and bridge
// services. Errors are non-fatal here: the services simply report no supported
// chains, and the transfer service (which loads its own copy) still works.
func loadSwapRegistries(baseDir string) (map[string]config.Network, map[string]map[string]config.Token, error) {
	networks, err := config.LoadNetworks(baseDir)
	if err != nil {
		return nil, nil, err
	}
	tokens, err := config.LoadTokens(baseDir)
	if err != nil {
		return networks, nil, err
	}
	return networks, tokens, nil
}

// chainResolver maps a Relay chain id back to a local network so a Relay step's
// chainId resolves to the correct RPC and chain id for signing.
func chainResolver(networks map[string]config.Network) func(relayChainID int64) (config.Network, bool) {
	return func(relayChainID int64) (config.Network, bool) {
		for _, network := range networks {
			if network.RelayChainID == relayChainID {
				return network, true
			}
		}
		return config.Network{}, false
	}
}
