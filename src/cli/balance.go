package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/ethereum/go-ethereum/common"

	balanceapp "multi-wallet-cli/app/balance"
	"multi-wallet-cli/cli/render"
	"multi-wallet-cli/cli/theme"
	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/i18n"
)

func (a *App) RunBalance(ctx context.Context, unlocked *wallet.WalletSet) error {
	printTitleBanner()
	if unlocked == nil || unlocked.Count() == 0 {
		return fmt.Errorf("no wallets available")
	}
	items := unlocked.Items()
	walletOptions := make([]string, 0, len(items))
	walletByLabel := make(map[string]wallet.PublicWallet, len(items))
	for _, item := range items {
		walletOptions = append(walletOptions, item.Label)
		walletByLabel[item.Label] = item
	}
	sort.Strings(walletOptions)
	selectedWallets := []string{}
	if err := survey.AskOne(&survey.MultiSelect{Message: i18n.T("balance.wallets"), Options: walletOptions, Default: walletOptions}, &selectedWallets, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	if len(selectedWallets) == 0 {
		return fmt.Errorf("select at least one wallet")
	}
	networks := a.balance.Networks()
	chainOptions := make([]string, 0, len(networks))
	for chain := range networks {
		chainOptions = append(chainOptions, chain)
	}
	sort.Strings(chainOptions)
	selectedChains := []string{}
	if err := survey.AskOne(&survey.MultiSelect{Message: i18n.T("balance.chains"), Options: chainOptions}, &selectedChains, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	if len(selectedChains) == 0 {
		return fmt.Errorf("select at least one chain")
	}
	tokenSet := map[string]bool{}
	for _, chain := range selectedChains {
		for symbol := range a.balance.Tokens()[chain] {
			tokenSet[symbol] = true
		}
	}
	tokenOptions := make([]string, 0, len(tokenSet))
	for symbol := range tokenSet {
		tokenOptions = append(tokenOptions, symbol)
	}
	sort.Strings(tokenOptions)
	tokenOptions = append([]string{i18n.T("balance.native")}, tokenOptions...)
	tokenOptions = append(tokenOptions, i18n.T("balance.custom_option"))
	selectedTokens := []string{}
	if err := survey.AskOne(&survey.MultiSelect{Message: i18n.T("balance.tokens"), Options: tokenOptions}, &selectedTokens, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	native := false
	customSelected := false
	selectedKnownTokens := make([]string, 0, len(selectedTokens))
	for _, selected := range selectedTokens {
		switch selected {
		case i18n.T("balance.native"):
			native = true
		case i18n.T("balance.custom_option"):
			customSelected = true
		default:
			selectedKnownTokens = append(selectedKnownTokens, selected)
		}
	}
	customCA := ""
	if customSelected {
		if err := survey.AskOne(&survey.Input{Message: i18n.T("balance.custom")}, &customCA, theme.SurveyOpts()...); err != nil {
			return handleSurveyError(err)
		}
		customCA = strings.TrimSpace(customCA)
	}
	custom, err := a.resolveBalanceCustoms(ctx, selectedChains, customCA)
	if err != nil {
		return err
	}
	wallets := make([]wallet.PublicWallet, 0, len(selectedWallets))
	for _, label := range selectedWallets {
		wallets = append(wallets, walletByLabel[label])
	}
	tokensByChain := make(map[string][]string)
	for _, chain := range selectedChains {
		tokensByChain[chain] = selectedKnownTokens
	}
	spin := startBalanceSpinner()
	report, err := a.balance.Scan(ctx, balanceapp.Request{Wallets: wallets, Chains: selectedChains, Tokens: tokensByChain, Native: native, Custom: custom})
	if err != nil {
		spin.Error(err.Error())
		return err
	}
	spin.Success(i18n.T("balance.complete"))
	printBalanceReport(report)
	return nil
}

// resolveBalanceCustoms reads the metadata (name, symbol, decimals) of a pasted
// contract on every selected EVM chain so the scan can track and render it
// instead of falling back to a placeholder symbol with no decimals.
func (a *App) resolveBalanceCustoms(ctx context.Context, chains []string, address string) (map[string]balanceapp.CustomToken, error) {
	normalized := ""
	if common.IsHexAddress(address) {
		normalized = common.HexToAddress(address).Hex()
	}
	if normalized == "" {
		if address != "" {
			return nil, fmt.Errorf("invalid EVM contract address")
		}
		return nil, nil
	}
	networks := a.balance.Networks()
	custom := map[string]balanceapp.CustomToken{}
	for _, chain := range chains {
		network, ok := networks[chain]
		if !ok || network.VMType != "evm" {
			continue
		}
		spin := render.StartSpinner(i18n.T("balance.custom.reading"))
		token, resolveErr := a.transfer.ResolveToken(ctx, chain, normalized)
		spin.Stop()
		if resolveErr != nil {
			return nil, resolveErr
		}
		custom[chain] = balanceapp.CustomToken{Symbol: token.Symbol, Name: token.Name, Address: token.Address, Decimals: token.Decimals}
		fmt.Println(theme.Info(fmt.Sprintf(i18n.T("balance.custom.read"), theme.Code(theme.TruncateMiddle(token.Address, 18)), token.Symbol, token.Decimals, chain)))
	}
	return custom, nil
}

func startBalanceSpinner() *render.Spinner { return render.StartSpinner(i18n.T("balance.scanning")) }

func printBalanceReport(report balanceapp.Report) {
	if len(report.Rows) == 0 {
		fmt.Println(theme.Info(i18n.T("balance.empty")))
		return
	}
	buildBalanceTable(report).Render()
}

// buildBalanceTable pivots the flat scan rows into one row per wallet and chain
// with a column per token, so the token columns are derived from the scan result
// instead of being hardcoded. Native balances share one column across chains and
// a chain-level failure collapses into a single errored row.
func buildBalanceTable(report balanceapp.Report) render.Table {
	hasNative := false
	seen := map[string]bool{}
	tokens := make([]string, 0)
	for _, row := range report.Rows {
		if row.Token == balanceapp.RowErrorToken && row.Err != nil {
			continue
		}
		if row.Native {
			hasNative = true
			continue
		}
		if seen[row.Token] {
			continue
		}
		seen[row.Token] = true
		tokens = append(tokens, row.Token)
	}
	sort.Strings(tokens)

	columns := make([]render.Column, 0, len(tokens)+4)
	columns = append(columns,
		render.Column{Header: i18n.T("balance.wallet"), Align: render.AlignLeft},
		render.Column{Header: i18n.T("balance.chain"), Align: render.AlignLeft},
	)
	if hasNative {
		columns = append(columns, render.Column{Header: i18n.T("balance.native_column"), Align: render.AlignRight})
	}
	for _, token := range tokens {
		columns = append(columns, render.Column{Header: token, Align: render.AlignRight})
	}
	columns = append(columns, render.Column{Header: i18n.T("balance.status"), Align: render.AlignLeft})

	type groupKey struct{ wallet, chain string }
	order := make([]groupKey, 0)
	type group struct {
		native    string
		hasNative bool
		cells     map[string]string
		err       error
	}
	groups := map[groupKey]*group{}
	for _, row := range report.Rows {
		key := groupKey{wallet: row.Wallet, chain: row.Chain}
		current, ok := groups[key]
		if !ok {
			current = &group{cells: map[string]string{}}
			groups[key] = current
			order = append(order, key)
		}
		if row.Token == balanceapp.RowErrorToken && row.Err != nil {
			current.err = row.Err
			continue
		}
		amount := balanceapp.FormatAmount(row.Raw, row.Decimals)
		if row.Native {
			current.native = amount
			current.hasNative = true
			continue
		}
		current.cells[row.Token] = amount
	}

	rows := make([]render.Row, 0, len(order))
	seenWallet := map[string]bool{}
	for _, key := range order {
		current := groups[key]
		cells := make([]string, 0, len(columns))
		first := !seenWallet[key.wallet]
		if first {
			cells = append(cells, theme.Value(key.wallet))
			seenWallet[key.wallet] = true
		} else {
			cells = append(cells, "")
		}
		cells = append(cells, key.chain)
		if hasNative {
			if current.hasNative {
				cells = append(cells, current.native)
			} else {
				cells = append(cells, theme.Dash(i18n.T("balance.not_applicable")))
			}
		}
		for _, token := range tokens {
			value, ok := current.cells[token]
			if !ok {
				cells = append(cells, theme.Dash(i18n.T("balance.not_applicable")))
				continue
			}
			cells = append(cells, value)
		}
		if current.err != nil {
			cells = append(cells, theme.Fail(i18n.T("balance.status.error")))
		} else {
			cells = append(cells, theme.Ok(i18n.T("balance.status.ok")))
		}
		rows = append(rows, render.Row{Cells: cells, First: first})
	}
	return render.Table{Columns: columns, Rows: rows}
}
