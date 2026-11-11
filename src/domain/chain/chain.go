package chain

import (
	"fmt"
	"strings"
)

// Chain is a validated chain slug present in networks.json.
type Chain struct {
	Slug string
}

// Parse validates slug against the known registry keys.
func Parse(slug string, known map[string]bool) (Chain, error) {
	trimmed := strings.ToLower(strings.TrimSpace(slug))
	if trimmed == "" {
		return Chain{}, fmt.Errorf("chain is required")
	}
	if known != nil {
		if _, ok := known[trimmed]; !ok {
			return Chain{}, fmt.Errorf("unknown chain %q", slug)
		}
	}
	return Chain{Slug: trimmed}, nil
}

// IsSolana reports whether the slug targets the Solana cluster.
func IsSolana(slug string) bool { return strings.ToLower(strings.TrimSpace(slug)) == "solana" }
