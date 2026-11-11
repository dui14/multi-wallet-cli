package configsvc

// knownExplorers maps well-known EVM chain IDs to their canonical block
// explorer base URL. A newly added EVM network resolves its explorer from the
// chain id reported by eth_chainId so transfer receipts link to a working
// explorer without asking the user to paste one.
var knownExplorers = map[int64]string{
	1:        "https://etherscan.io/",
	10:       "https://optimistic.etherscan.io/",
	56:       "https://bscscan.com/",
	97:       "https://testnet.bscscan.com/",
	137:      "https://polygonscan.com/",
	250:      "https://ftmscan.com/",
	8453:     "https://basescan.org/",
	42161:    "https://arbiscan.io/",
	43114:    "https://snowtrace.io/",
	59144:    "https://lineascan.build/",
	11155111: "https://sepolia.etherscan.io/",
	80002:    "https://amoy.polygonscan.com/",
	84532:    "https://sepolia.basescan.org/",
	421614:   "https://sepolia.arbiscan.io/",
	11155420: "https://sepolia-optimism.etherscan.io/",
}

// ExplorerForChainID returns the known explorer base URL for an EVM chain id,
// or an empty string when the chain is not in the registry.
func ExplorerForChainID(chainID int64) string {
	return knownExplorers[chainID]
}
