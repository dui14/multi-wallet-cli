package configsvc

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"multi-wallet-cli/domain/errcodes"
	"multi-wallet-cli/infra/config"
	"multi-wallet-cli/infra/rpc"
)

// NetworkValidator verifies RPC reachability and resolves a VM's native network
// identity so duplicate networks can be detected before they are persisted.
// Each VM type supplies its own implementation; VM types without a registered
// validator are saved without live checks and that limitation is surfaced to the
// user.
type NetworkValidator interface {
	// ValidateRPCs connects to the primary RPC and every seed RPC, confirms each
	// reachable endpoint reports a consistent identity matching the draft when
	// the draft declares one, and returns the resolved identity. It returns an
	// empty identity when no endpoint can be probed live (for example when every
	// endpoint is a websocket URL).
	ValidateRPCs(ctx context.Context, draft NetworkDraft, rpcs []string) (string, error)
	// Identity reads the native network identity reported by a single endpoint.
	Identity(ctx context.Context, rpcURL string) (string, error)
	// StoredIdentity returns the identity already recorded for a network when the
	// VM type persists a reliable one. The second result is false when the
	// identity must be read live from the network RPC.
	StoredIdentity(network config.Network) (string, bool)
}

// ValidatorRegistry resolves the NetworkValidator for a VM type.
type ValidatorRegistry interface {
	Validator(vmType string) (NetworkValidator, bool)
}

// DefaultValidators builds the registry of VM validators known to the tool.
func DefaultValidators() ValidatorRegistry {
	return vmRegistry{prober: rpc.NewProber(nil)}
}

type vmRegistry struct{ prober *rpc.Prober }

func (r vmRegistry) Validator(vmType string) (NetworkValidator, bool) {
	switch vmType {
	case "evm":
		return evmValidator{prober: r.prober}, true
	case "svm":
		return svmValidator{prober: r.prober}, true
	default:
		return nil, false
	}
}

func isLiveProbe(endpoint string) bool {
	lower := strings.ToLower(endpoint)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

type evmValidator struct{ prober *rpc.Prober }

func (evmValidator) StoredIdentity(network config.Network) (string, bool) {
	if network.ChainID == 0 {
		return "", false
	}
	return strconv.FormatInt(network.ChainID, 10), true
}

func (v evmValidator) Identity(ctx context.Context, rpcURL string) (string, error) {
	chainID, err := v.prober.EVMChainID(ctx, rpcURL)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(chainID, 10), nil
}

func (v evmValidator) ValidateRPCs(ctx context.Context, draft NetworkDraft, rpcs []string) (string, error) {
	identity := ""
	for index, endpoint := range rpcs {
		if !isLiveProbe(endpoint) {
			continue
		}
		chainID, err := v.prober.EVMChainID(ctx, endpoint)
		if err != nil {
			return "", fmt.Errorf("rpc %d unreachable or invalid: %w", index+1, err)
		}
		if draft.ChainID != 0 && chainID != draft.ChainID {
			return "", fmt.Errorf("rpc %d reports chain id %d but the network declares %d", index+1, chainID, draft.ChainID)
		}
		resolved := strconv.FormatInt(chainID, 10)
		if identity == "" {
			identity = resolved
		} else if identity != resolved {
			return "", fmt.Errorf("rpc %d reports chain id %s but an earlier rpc reported %s", index+1, resolved, identity)
		}
	}
	return identity, nil
}

type svmValidator struct{ prober *rpc.Prober }

func (svmValidator) StoredIdentity(config.Network) (string, bool) {
	return "", false
}

func (v svmValidator) Identity(ctx context.Context, rpcURL string) (string, error) {
	return v.prober.SolanaGenesisHash(ctx, rpcURL)
}

func (v svmValidator) ValidateRPCs(ctx context.Context, _ NetworkDraft, rpcs []string) (string, error) {
	identity := ""
	for index, endpoint := range rpcs {
		if !isLiveProbe(endpoint) {
			continue
		}
		genesis, err := v.prober.SolanaGenesisHash(ctx, endpoint)
		if err != nil {
			return "", fmt.Errorf("rpc %d unreachable or invalid: %w", index+1, err)
		}
		if identity == "" {
			identity = genesis
		} else if identity != genesis {
			return "", fmt.Errorf("rpc %d reports a different network genesis hash", index+1)
		}
	}
	return identity, nil
}

// validateNetwork runs the VM validator for the draft and rejects duplicate
// identities before the network is written. It returns the resolved native
// identity (for EVM the chain id reported by eth_chainId) so the caller can
// derive the block explorer. VM types without a validator are accepted without
// live checks and report an empty identity.
func (s *Service) validateNetwork(ctx context.Context, networks map[string]config.Network, slug, vmType string, draft NetworkDraft, network config.Network, rpcs []string) (string, error) {
	if s.validators == nil {
		return "", nil
	}
	validator, ok := s.validators.Validator(vmType)
	if !ok {
		return "", nil
	}
	identity, err := validator.ValidateRPCs(ctx, draft, rpcs)
	if err != nil {
		return "", fmt.Errorf("%w: %s", errcodes.ErrInvalidInput, err)
	}
	if identity == "" {
		if declared, ok := validator.StoredIdentity(network); ok {
			identity = declared
		}
	}
	if identity == "" {
		return "", nil
	}
	for otherSlug, other := range networks {
		if otherSlug == slug || !strings.EqualFold(other.VMType, vmType) {
			continue
		}
		otherIdentity, ok := validator.StoredIdentity(other)
		if !ok {
			endpoint := other.PrimaryRPC()
			if endpoint == "" {
				continue
			}
			otherIdentity, err = validator.Identity(ctx, endpoint)
			if err != nil {
				continue
			}
		}
		if otherIdentity != "" && otherIdentity == identity {
			return "", fmt.Errorf("%w: network identity already registered as %q", errcodes.ErrInvalidInput, otherSlug)
		}
	}
	return identity, nil
}
