package cli

import "errors"

var (
	errNoWallets           = errors.New("no wallets available")
	errNoEvmWallets        = errors.New("no EVM wallets available")
	errTransferUnavailable = errors.New("transfer service is not ready")
	errChainNotFound       = errors.New("chain not found")
	errSwapUnavailable     = errors.New("swap service is not ready")
	errBridgeUnavailable   = errors.New("bridge service is not ready")
)
