package cli

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/AlecAivazis/survey/v2"

	transferapp "multi-wallet-cli/app/transfer"
	"multi-wallet-cli/cli/theme"
	"multi-wallet-cli/domain/amount"
	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/i18n"
)

func (a *App) askTransferSource(unlocked *wallet.WalletSet) (wallet.PublicWallet, error) {
	byLabel := a.evmWalletMap(unlocked)
	labels := sortedKeys(byLabel)
	if len(labels) == 0 {
		return wallet.PublicWallet{}, errNoEvmWallets
	}
	choice := ""
	if err := survey.AskOne(&survey.Select{Message: i18n.T("transfer.from"), Options: labels}, &choice, theme.SurveyOpts()...); err != nil {
		return wallet.PublicWallet{}, handleSurveyError(err)
	}
	return byLabel[choice], nil
}

func (a *App) askTransferSources(unlocked *wallet.WalletSet) ([]wallet.PublicWallet, error) {
	byLabel := a.evmWalletMap(unlocked)
	labels := sortedKeys(byLabel)
	if len(labels) == 0 {
		return nil, errNoEvmWallets
	}
	selected := []string{}
	if err := survey.AskOne(&survey.MultiSelect{Message: i18n.T("transfer.sources"), Options: labels}, &selected, theme.SurveyOpts()...); err != nil {
		return nil, handleSurveyError(err)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("select at least one source wallet")
	}
	out := make([]wallet.PublicWallet, 0, len(selected))
	for _, label := range selected {
		out = append(out, byLabel[label])
	}
	return out, nil
}

func (a *App) evmWalletMap(unlocked *wallet.WalletSet) map[string]wallet.PublicWallet {
	byLabel := map[string]wallet.PublicWallet{}
	for _, item := range unlocked.Items() {
		if item.ChainKind == wallet.ChainKindEVM {
			byLabel[walletChoiceLabel(item)] = item
		}
	}
	return byLabel
}

func sortedKeys(items map[string]wallet.PublicWallet) []string {
	labels := make([]string, 0, len(items))
	for label := range items {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return labels
}

func walletChoiceLabel(item wallet.PublicWallet) string {
	short := theme.TruncateMiddle(item.Address, 14)
	if item.Label == "" {
		return short
	}
	return item.Label + " · " + short
}

func (a *App) askTransferTokens(ctx context.Context, draft transferDraft, owner wallet.PublicWallet) ([]transferapp.TokenLine, error) {
	lines, err := a.askTransferTokenChoices(ctx, draft, owner, true)
	if err != nil {
		return nil, err
	}
	for index := range lines {
		if askErr := a.askTokenAmount(&lines[index]); askErr != nil {
			return nil, askErr
		}
	}
	return lines, nil
}

func (a *App) askTransferTokenChoices(ctx context.Context, draft transferDraft, owner wallet.PublicWallet, withBalance bool) ([]transferapp.TokenLine, error) {
	options, byLabel, err := a.tokenChoiceOptions(ctx, draft, owner, withBalance)
	if err != nil {
		return nil, err
	}
	selected := []string{}
	if err := survey.AskOne(&survey.MultiSelect{Message: i18n.T("transfer.tokens"), Options: options}, &selected, theme.SurveyOpts()...); err != nil {
		return nil, handleSurveyError(err)
	}
	lines := make([]transferapp.TokenLine, 0, len(selected))
	custom := false
	for _, choice := range selected {
		if choice == i18n.T("transfer.custom_option") {
			custom = true
			continue
		}
		lines = append(lines, byLabel[choice])
	}
	if custom {
		customLine, customErr := a.askCustomToken(ctx, draft)
		if customErr != nil {
			return nil, customErr
		}
		lines = append(lines, customLine)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("select at least one token")
	}
	return orderNativeLast(lines), nil
}

func (a *App) tokenChoiceOptions(ctx context.Context, draft transferDraft, owner wallet.PublicWallet, withBalance bool) ([]string, map[string]transferapp.TokenLine, error) {
	var lines []transferapp.TokenLine
	var err error
	if withBalance {
		lines, err = a.loadOwnerTokens(ctx, draft, owner)
		if err != nil {
			return nil, nil, err
		}
	} else {
		lines = a.registryTokenLines(draft)
	}
	options := make([]string, 0, len(lines)+1)
	byLabel := map[string]transferapp.TokenLine{}
	for _, line := range lines {
		label := tokenChoiceLabel(line, draft.network.NativeSymbol)
		options = append(options, label)
		byLabel[label] = line
	}
	sort.Strings(options)
	options = append(options, i18n.T("transfer.custom_option"))
	return options, byLabel, nil
}

func tokenChoiceLabel(line transferapp.TokenLine, nativeSymbol string) string {
	if line.Native {
		return fmt.Sprintf(i18n.T("transfer.native_option"), nativeSymbol)
	}
	if line.Balance != nil {
		shown := "?"
		if line.Decimals > 0 {
			shown = amount.Format(line.Balance, line.Decimals)
		}
		return fmt.Sprintf("%s · %s", line.Symbol, shown)
	}
	return line.Symbol
}

func (a *App) registryTokenLines(draft transferDraft) []transferapp.TokenLine {
	registry := a.transfer.Tokens()[draft.chain]
	lines := make([]transferapp.TokenLine, 0, len(registry)+1)
	symbols := make([]string, 0, len(registry))
	for symbol := range registry {
		if strings.EqualFold(symbol, "NATIVE") {
			continue
		}
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	if draft.network.VMType == "evm" {
		lines = append(lines, transferapp.TokenLine{Symbol: draft.network.NativeSymbol, Native: true, Decimals: draft.network.NativeDecimals})
	}
	for _, symbol := range symbols {
		token := registry[symbol]
		lines = append(lines, transferapp.TokenLine{Symbol: token.Symbol, Address: token.Address, Decimals: token.Decimals})
	}
	return lines
}

func (a *App) loadOwnerTokens(ctx context.Context, draft transferDraft, owner wallet.PublicWallet) ([]transferapp.TokenLine, error) {
	lines := a.registryTokenLines(draft)
	for index := range lines {
		balance, err := a.walletTokenBalance(ctx, draft, owner, lines[index])
		if err != nil {
			continue
		}
		lines[index].Balance = balance
	}
	return lines, nil
}

func (a *App) attachTokenBalances(ctx context.Context, draft transferDraft, owner wallet.PublicWallet, lines []transferapp.TokenLine) error {
	for index := range lines {
		balance, err := a.walletTokenBalance(ctx, draft, owner, lines[index])
		if err != nil {
			return err
		}
		lines[index].Balance = balance
	}
	return nil
}

func (a *App) walletTokenBalance(ctx context.Context, draft transferDraft, owner wallet.PublicWallet, line transferapp.TokenLine) (*big.Int, error) {
	flow := a.transfer.BalancesFor(draft.chain, owner)
	address := ""
	if !line.Native {
		address = line.Address
	}
	return flow.TokenBalance(ctx, address)
}

func (a *App) tightestBalance(ctx context.Context, draft transferDraft, owners []wallet.PublicWallet, line transferapp.TokenLine) (*big.Int, error) {
	var tight *big.Int
	for _, owner := range owners {
		balance, err := a.walletTokenBalance(ctx, draft, owner, line)
		if err != nil {
			return nil, err
		}
		if tight == nil || balance.Cmp(tight) < 0 {
			tight = balance
		}
	}
	return tight, nil
}

func (a *App) askTokenAmount(item *transferapp.TokenLine) error {
	label := fmt.Sprintf(i18n.T("transfer.amount_for"), item.Symbol)
	value, blank, err := a.askBoundedAmount(item.Symbol, item.Decimals, item.Balance, 1, label, true)
	if err != nil {
		return err
	}
	if blank {
		item.Max = true
		item.Amount = nil
		return nil
	}
	item.Max = false
	item.Amount = value
	return nil
}

func (a *App) askBoundedAmount(symbol string, decimals uint8, balance *big.Int, count int, label string, allowBlank bool) (*big.Int, bool, error) {
	for {
		raw := ""
		if err := survey.AskOne(&survey.Input{Message: label}, &raw, theme.SurveyOpts()...); err != nil {
			return nil, false, handleSurveyError(err)
		}
		trimmed := strings.TrimSpace(raw)
		if allowBlank && (trimmed == "" || strings.EqualFold(trimmed, i18n.T("transfer.max"))) {
			return nil, true, nil
		}
		value, err := amount.Parse(trimmed, decimals)
		if err != nil {
			fmt.Println(theme.Error(symbol + ": " + err.Error()))
			continue
		}
		check := amount.LessThanBalance(value, balance)
		if count > 1 {
			check = amount.RepeatedLessThanBalance(value, count, balance)
		}
		if check != nil {
			fmt.Println(theme.Error(amountRejectText(symbol, balance, decimals, check)))
			continue
		}
		return value, false, nil
	}
}

func amountRejectText(symbol string, balance *big.Int, decimals uint8, err error) string {
	shown := "-"
	if balance != nil {
		shown = amount.Format(balance, decimals)
	}
	switch {
	case errors.Is(err, amount.ErrTotalNotBelowBalance):
		return i18n.T("transfer.amount_total_below", symbol, shown)
	case errors.Is(err, amount.ErrBalanceUnavailable):
		return i18n.T("transfer.balance_unavailable", symbol)
	default:
		return i18n.T("transfer.amount_below_balance", symbol, shown)
	}
}

func (a *App) askCustomToken(ctx context.Context, draft transferDraft) (transferapp.TokenLine, error) {
	address := ""
	if err := survey.AskOne(&survey.Input{Message: i18n.T("transfer.custom")}, &address, theme.SurveyOpts()...); err != nil {
		return transferapp.TokenLine{}, handleSurveyError(err)
	}
	meta, err := a.transfer.ResolveToken(ctx, draft.chain, strings.TrimSpace(address))
	if err != nil {
		return transferapp.TokenLine{}, err
	}
	printCustomTokenSummary(draft.chain, meta)
	return meta, nil
}

func printCustomTokenSummary(chain string, token transferapp.TokenLine) {
	fmt.Println(theme.Success(i18n.T("transfer.checked")))
	fmt.Println()
	fmt.Println(theme.Title(i18n.T("transfer.summary")))
	fmt.Println(summaryRow(i18n.T("config.token.chain"), chain))
	fmt.Println(summaryRow(i18n.T("config.token.address"), token.Address))
	fmt.Println(summaryRow(i18n.T("config.token.name"), token.Name))
	fmt.Println(summaryRow(i18n.T("config.token.symbol"), token.Symbol))
	fmt.Println(summaryRow(i18n.T("config.token.decimals"), fmt.Sprintf("%d", token.Decimals)))
	fmt.Println(theme.Warning(i18n.T("config.token_warning")))
	fmt.Println()
}

func orderNativeLast(lines []transferapp.TokenLine) []transferapp.TokenLine {
	natives := make([]transferapp.TokenLine, 0)
	others := make([]transferapp.TokenLine, 0, len(lines))
	for _, line := range lines {
		if line.Native {
			natives = append(natives, line)
			continue
		}
		others = append(others, line)
	}
	return append(others, natives...)
}
