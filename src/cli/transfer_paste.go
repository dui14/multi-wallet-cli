package cli

import (
	"errors"
	"fmt"
	"strings"

	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/i18n"
)

var errPasteSecurity = errors.New("transfer paste canceled")

func inspectPastedAddresses(raw string, targetVM string) ([]string, string, bool) {
	if strings.TrimSpace(raw) == "" {
		return nil, "", false
	}
	if wallet.ContainsPastedSecret(raw) {
		return nil, i18n.T("transfer.paste.secret"), true
	}
	target := canonicalVM(targetVM)
	items := parseAddressList(raw)
	issues := make([]pasteIssue, 0)
	for _, item := range items {
		detected := wallet.AddressVM(item)
		if detected == target {
			continue
		}
		if detected != "" {
			issues = append(issues, pasteIssue{item: item, reason: i18n.T("transfer.paste.wrong_network", vmLabel(detected), vmLabel(target))})
			continue
		}
		issues = append(issues, pasteIssue{item: item, reason: formatReason(item, target)})
	}
	if len(issues) == 0 {
		return items, "", false
	}
	return nil, formatPasteIssues(issues), false
}

type pasteIssue struct {
	item   string
	reason string
}

func formatPasteIssues(issues []pasteIssue) string {
	lines := make([]string, 0, len(issues)+1)
	lines = append(lines, i18n.T("transfer.paste.invalid_title"))
	for _, issue := range issues {
		lines = append(lines, fmt.Sprintf("- %s: %s", truncateAddress(issue.item), issue.reason))
	}
	return strings.Join(lines, "\n")
}

func formatReason(item string, target string) string {
	switch target {
	case wallet.VMKindSolana:
		return i18n.T("transfer.paste.solana_format")
	case wallet.VMKindTron:
		return i18n.T("transfer.paste.tron_format")
	default:
		return evmFormatReason(item)
	}
}

func evmFormatReason(item string) string {
	if len(item) < 42 {
		return i18n.T("transfer.paste.evm_short")
	}
	if len(item) > 42 {
		return i18n.T("transfer.paste.evm_long")
	}
	return i18n.T("transfer.paste.evm_hex")
}

func canonicalVM(vm string) string {
	switch strings.ToLower(strings.TrimSpace(vm)) {
	case "solana", wallet.VMKindSolana:
		return wallet.VMKindSolana
	case "tvm", wallet.VMKindTron:
		return wallet.VMKindTron
	default:
		return wallet.VMKindEVM
	}
}

func vmLabel(vm string) string {
	switch canonicalVM(vm) {
	case wallet.VMKindSolana:
		return "Solana"
	case wallet.VMKindTron:
		return "Tron"
	default:
		return "EVM"
	}
}

func truncateAddress(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > 8 {
		return string(runes[:4]) + "..." + string(runes[len(runes)-4:])
	}
	if len(runes) <= 2 {
		return "...."
	}
	return string(runes[:1]) + "..." + string(runes[len(runes)-1:])
}
