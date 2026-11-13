package wallet

import "strings"

const (
	VMKindEVM    = "evm"
	VMKindSolana = "svm"
	VMKindTron   = "tron"
)

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// ContainsPastedSecret reports whether raw contains a private key or a 12 or 24 word seed.
func ContainsPastedSecret(raw string) bool {
	if hasSeedRun(raw) {
		return true
	}
	for _, item := range splitPastedItems(raw) {
		if isPrivateKey(item) || hasSeedRun(item) {
			return true
		}
	}
	return false
}

// AddressVM reports the chain family an item matches: evm, svm, tron, or empty.
func AddressVM(item string) string {
	trimmed := strings.TrimSpace(item)
	switch {
	case isEVMAddress(trimmed):
		return VMKindEVM
	case isTronAddress(trimmed):
		return VMKindTron
	case isSolanaAddress(trimmed):
		return VMKindSolana
	default:
		return ""
	}
}

func splitPastedItems(raw string) []string {
	replacer := strings.NewReplacer(",", "\n", ";", "\n", " ", "\n")
	parts := strings.Split(replacer.Replace(raw), "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func isPrivateKey(item string) bool {
	trimmed := strings.TrimSpace(item)
	hexPart := trimmed
	if strings.HasPrefix(strings.ToLower(trimmed), "0x") {
		hexPart = trimmed[2:]
		if len(hexPart) != 64 {
			return false
		}
		return isHex(hexPart)
	}
	return len(hexPart) == 64 && isHex(hexPart)
}

func isEVMAddress(item string) bool {
	if len(item) != 42 || !strings.HasPrefix(strings.ToLower(item), "0x") {
		return false
	}
	return isHex(item[2:])
}

func isSolanaAddress(item string) bool {
	if len(item) < 32 || len(item) > 44 {
		return false
	}
	if strings.HasPrefix(strings.ToLower(item), "0x") {
		return false
	}
	return isBase58(item)
}

func isTronAddress(item string) bool {
	return len(item) == 34 && strings.HasPrefix(item, "T") && isBase58(item)
}

func isHex(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		switch {
		case ch >= '0' && ch <= '9':
		case ch >= 'a' && ch <= 'f':
		case ch >= 'A' && ch <= 'F':
		default:
			return false
		}
	}
	return true
}

func isBase58(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if !strings.ContainsRune(base58Alphabet, ch) {
			return false
		}
	}
	return true
}

func hasSeedRun(raw string) bool {
	normalized := strings.NewReplacer(",", " ", ";", " ", "\n", " ", "\r", " ", "\t", " ").Replace(raw)
	run := 0
	for _, field := range strings.Fields(normalized) {
		if isAlphaWord(field) {
			run++
			if run == 12 || run == 24 {
				return true
			}
			continue
		}
		run = 0
	}
	return false
}

func isAlphaWord(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < 'A' || (ch > 'Z' && ch < 'a') || ch > 'z' {
			return false
		}
	}
	return true
}
