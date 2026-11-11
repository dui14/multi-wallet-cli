package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/AlecAivazis/survey/v2"

	swapapp "multi-wallet-cli/app/swap"
	"multi-wallet-cli/cli/render"
	"multi-wallet-cli/cli/theme"
	"multi-wallet-cli/domain/amount"
	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/config"
	"multi-wallet-cli/infra/i18n"
)

func (a *App) RunSwap(ctx context.Context, unlocked *wallet.WalletSet) error {
	printTitleBanner()
	if unlocked == nil || unlocked.Count() == 0 {
		return errNoWallets
	}
	if a.swap == nil {
		return errSwapUnavailable
	}
	chains := a.swap.SupportedChains()
	if len(chains) == 0 {
		fmt.Println(theme.Warning(i18n.T("swap.no_chains")))
		return nil
	}
	chain := ""
	if err := survey.AskOne(&survey.Select{Message: i18n.T("swap.chain"), Options: chains}, &chain, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	owner, err := a.askTransferSource(unlocked)
	if err != nil {
		return err
	}
	fromToken, err := a.askSwapToken(chain, i18n.T("swap.from_token"))
	if err != nil {
		return err
	}
	toToken, err := a.askSwapToken(chain, i18n.T("swap.to_token"))
	if err != nil {
		return err
	}
	raw := ""
	if err := survey.AskOne(&survey.Input{Message: fmt.Sprintf(i18n.T("swap.amount"), fromToken.Symbol)}, &raw, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	value, err := amount.Parse(strings.TrimSpace(raw), fromToken.Decimals)
	if err != nil {
		return fmt.Errorf("%s: %w", fromToken.Symbol, err)
	}
	recipient := ""
	if err := survey.AskOne(&survey.Input{Message: i18n.T("swap.recipient")}, &recipient, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	request := swapapp.Request{
		Chain:        chain,
		From:         owner,
		FromToken:    fromToken,
		ToToken:      toToken,
		Amount:       value,
		Recipient:    strings.TrimSpace(recipient),
		Slippage:     "0.005",
		FromIsNative: fromToken.Address == "",
		ToIsNative:   toToken.Address == "",
	}
	spin := render.StartSpinner(i18n.T("swap.fetching"))
	quote, quoteErr := a.swap.Quote(ctx, request)
	spin.Stop()
	if quoteErr != nil {
		return quoteErr
	}
	printSwapSummary(chain, fromToken, toToken, quote)
	if !a.confirmProceed() {
		return nil
	}
	spin = render.StartSpinner(i18n.T("swap.executing"))
	result := a.swap.Do(ctx, request)
	spin.Stop()
	printSwapResult(result)
	return result.Err
}

func (a *App) askSwapToken(chain string, prompt string) (config.Token, error) {
	network := a.swap.Networks()[chain]
	registry := a.swap.Tokens()[chain]
	symbols := make([]string, 0, len(registry)+1)
	for symbol := range registry {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	nativeLabel := fmt.Sprintf(i18n.T("swap.native"), network.NativeSymbol)
	options := append([]string{nativeLabel}, symbols...)
	choice := ""
	if err := survey.AskOne(&survey.Select{Message: prompt, Options: options}, &choice, theme.SurveyOpts()...); err != nil {
		return config.Token{}, handleSurveyError(err)
	}
	if choice == nativeLabel {
		return config.Token{Symbol: network.NativeSymbol, Decimals: network.NativeDecimals}, nil
	}
	return registry[choice], nil
}

func printSwapSummary(chain string, from config.Token, to config.Token, quote *swapapp.Quote) {
	fmt.Println()
	fmt.Println(theme.Title(i18n.T("swap.summary")))
	fmt.Println(summaryRow(i18n.T("swap.chain"), chain))
	fmt.Println(summaryRow(i18n.T("swap.pair"), from.Symbol+" -> "+quote.ReceiveSymbol+" ("+to.Symbol+")"))
	if quote.ExpectedReceive != "" {
		fmt.Println(summaryRow(i18n.T("swap.expected"), quote.ExpectedReceive))
	}
	fmt.Println(summaryRow(i18n.T("swap.gas_fee"), quote.GasFee))
	fmt.Println(summaryRow(i18n.T("swap.relayer_fee"), quote.RelayerFee))
	fmt.Println(summaryRow(i18n.T("swap.steps"), fmt.Sprintf("%d", quote.Steps)))
	fmt.Println()
}

func printSwapResult(result swapapp.Result) {
	fmt.Println()
	if result.Err != nil {
		fmt.Println(theme.Error(fmt.Sprintf(i18n.T("swap.failed"), result.Wallet, result.ErrorCode)))
		return
	}
	fmt.Println(theme.Success(fmt.Sprintf(i18n.T("swap.done"), result.Wallet, result.Token)))
	if result.SourceTxHash != "" {
		fmt.Println("    " + theme.Code(result.SourceTxHash))
	}
	fmt.Println("    " + theme.Label(i18n.T("swap.status")+": "+string(result.FinalStatus)))
	fmt.Println()
}

func (a *App) confirmProceed() bool {
	choice := false
	if err := survey.AskOne(&survey.Confirm{Message: i18n.T("swap.confirm"), Default: false}, &choice, theme.SurveyOpts()...); err != nil {
		return false
	}
	return choice
}
