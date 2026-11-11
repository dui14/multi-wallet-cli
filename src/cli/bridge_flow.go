package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/AlecAivazis/survey/v2"

	bridgeapp "multi-wallet-cli/app/bridge"
	"multi-wallet-cli/cli/render"
	"multi-wallet-cli/cli/theme"
	"multi-wallet-cli/domain/amount"
	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/config"
	"multi-wallet-cli/infra/i18n"
)

func (a *App) RunBridge(ctx context.Context, unlocked *wallet.WalletSet) error {
	printTitleBanner()
	if unlocked == nil || unlocked.Count() == 0 {
		return errNoWallets
	}
	if a.bridge == nil {
		return errBridgeUnavailable
	}
	chains := a.bridge.SupportedChains()
	if len(chains) < 2 {
		fmt.Println(theme.Warning(i18n.T("bridge.no_chains")))
		return nil
	}
	fromChain := ""
	if err := survey.AskOne(&survey.Select{Message: i18n.T("bridge.from_chain"), Options: chains}, &fromChain, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	toChains := make([]string, 0, len(chains))
	for _, chain := range chains {
		if chain != fromChain {
			toChains = append(toChains, chain)
		}
	}
	toChain := ""
	if err := survey.AskOne(&survey.Select{Message: i18n.T("bridge.to_chain"), Options: toChains}, &toChain, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	owner, err := a.askTransferSource(unlocked)
	if err != nil {
		return err
	}
	fromToken, err := a.askBridgeToken(fromChain, i18n.T("bridge.from_token"))
	if err != nil {
		return err
	}
	toToken, err := a.askBridgeToken(toChain, i18n.T("bridge.to_token"))
	if err != nil {
		return err
	}
	raw := ""
	if err := survey.AskOne(&survey.Input{Message: fmt.Sprintf(i18n.T("bridge.amount"), fromToken.Symbol)}, &raw, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	value, err := amount.Parse(strings.TrimSpace(raw), fromToken.Decimals)
	if err != nil {
		return fmt.Errorf("%s: %w", fromToken.Symbol, err)
	}
	recipient := ""
	if err := survey.AskOne(&survey.Input{Message: i18n.T("bridge.recipient")}, &recipient, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	request := bridgeapp.Request{
		FromChain:    fromChain,
		ToChain:      toChain,
		From:         owner,
		FromToken:    fromToken,
		ToToken:      toToken,
		Amount:       value,
		Recipient:    strings.TrimSpace(recipient),
		Slippage:     "0.005",
		FromIsNative: fromToken.Address == "",
		ToIsNative:   toToken.Address == "",
	}
	spin := render.StartSpinner(i18n.T("bridge.fetching"))
	quote, quoteErr := a.bridge.Quote(ctx, request)
	spin.Stop()
	if quoteErr != nil {
		return quoteErr
	}
	printBridgeSummary(fromChain, toChain, fromToken, toToken, quote)
	if !a.confirmProceed() {
		return nil
	}
	spin = render.StartSpinner(i18n.T("bridge.executing"))
	result := a.bridge.Do(ctx, request)
	spin.Stop()
	printBridgeResult(result)
	return result.Err
}

func (a *App) askBridgeToken(chain string, prompt string) (config.Token, error) {
	network := a.bridge.Networks()[chain]
	registry := a.bridge.Tokens()[chain]
	symbols := make([]string, 0, len(registry)+1)
	for symbol := range registry {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	nativeLabel := fmt.Sprintf(i18n.T("bridge.native"), network.NativeSymbol)
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

func printBridgeSummary(fromChain string, toChain string, from config.Token, to config.Token, quote *bridgeapp.Quote) {
	fmt.Println()
	fmt.Println(theme.Title(i18n.T("bridge.summary")))
	fmt.Println(summaryRow(i18n.T("bridge.route"), fromChain+" -> "+toChain))
	fmt.Println(summaryRow(i18n.T("bridge.pair"), from.Symbol+" -> "+quote.ReceiveSymbol+" ("+to.Symbol+")"))
	if quote.CrossAsset {
		fmt.Println(theme.Warning(i18n.T("bridge.cross_asset")))
	}
	if quote.ExpectedReceive != "" {
		fmt.Println(summaryRow(i18n.T("bridge.expected"), quote.ExpectedReceive))
	}
	fmt.Println(summaryRow(i18n.T("bridge.gas_fee"), quote.GasFee))
	fmt.Println(summaryRow(i18n.T("bridge.relayer_fee"), quote.RelayerFee))
	fmt.Println()
}

func printBridgeResult(result bridgeapp.Result) {
	fmt.Println()
	if result.Err != nil {
		fmt.Println(theme.Error(fmt.Sprintf(i18n.T("bridge.failed"), result.Wallet, result.ErrorCode)))
		return
	}
	fmt.Println(theme.Success(fmt.Sprintf(i18n.T("bridge.done"), result.Wallet, result.Token)))
	if result.SourceTxHash != "" {
		fmt.Println("    " + theme.Code(result.SourceTxHash))
	}
	fmt.Println("    " + theme.Label(i18n.T("bridge.status")+": "+string(result.FinalStatus)))
	fmt.Println()
}
