package cli

import (
	"sort"
	"strings"

	"github.com/AlecAivazis/survey/v2"

	"multi-wallet-cli/cli/theme"
	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/i18n"
)

// destinationOption pairs a display label with the EVM address it resolves to.
type destinationOption struct {
	label   string
	address string
}

// destinationWalletOptions lists unlocked EVM wallets as "alias · 0x7aDD…96b8CdE"
// entries, deduplicated by address and sorted, so a transfer, swap, or bridge
// flow can offer saved wallets as destinations without exposing raw addresses.
func (a *App) destinationWalletOptions(unlocked *wallet.WalletSet) []destinationOption {
	if unlocked == nil {
		return nil
	}
	seen := map[string]bool{}
	options := make([]destinationOption, 0)
	for _, item := range unlocked.Items() {
		if item.ChainKind != wallet.ChainKindEVM {
			continue
		}
		key := strings.ToLower(item.Address)
		if seen[key] {
			continue
		}
		seen[key] = true
		options = append(options, destinationOption{label: walletChoiceLabel(item), address: item.Address})
	}
	sort.Slice(options, func(i, j int) bool { return options[i].label < options[j].label })
	return options
}

// askDestinationSingle offers saved wallets plus a manual-entry fallback and
// returns one destination address. Selecting the manual option opens an input
// step where Enter returns to the wallet list. Used by one-to-one and
// many-to-one, which each send to a single destination.
func (a *App) askDestinationSingle(unlocked *wallet.WalletSet) (string, error) {
	options := a.destinationWalletOptions(unlocked)
	labels := make([]string, 0, len(options)+1)
	byLabel := make(map[string]string, len(options))
	for _, option := range options {
		labels = append(labels, option.label)
		byLabel[option.label] = option.address
	}
	manual := i18n.T("transfer.destination.manual")
	labels = append(labels, manual)
	choice := ""
	if err := survey.AskOne(&survey.Select{Message: i18n.T("transfer.destination"), Options: labels}, &choice, theme.SurveyOpts()...); err != nil {
		return "", handleSurveyError(err)
	}
	if choice != manual {
		return byLabel[choice], nil
	}
	raw := ""
	if err := survey.AskOne(&survey.Input{Message: i18n.T("transfer.destination.input")}, &raw, theme.SurveyOpts()...); err != nil {
		return "", handleSurveyError(err)
	}
	address := strings.TrimSpace(raw)
	if address == "" {
		return a.askDestinationSingle(unlocked)
	}
	return address, nil
}
