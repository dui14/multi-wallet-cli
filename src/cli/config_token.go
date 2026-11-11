package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/AlecAivazis/survey/v2"

	"multi-wallet-cli/cli/render"
	"multi-wallet-cli/cli/theme"
	"multi-wallet-cli/infra/config"
	"multi-wallet-cli/infra/i18n"
)

func (a *App) runManageTokens(ctx context.Context) error {
	for {
		choice, err := a.askManageTokensMenu()
		if err != nil {
			return err
		}
		switch choice {
		case manageAdd:
			if err := a.runAddToken(ctx); err != nil {
				return err
			}
		case manageDelete:
			if err := a.runDeleteToken(ctx); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func (a *App) askManageTokensMenu() (string, error) {
	options := []string{i18n.T("config.add_token"), i18n.T("config.delete_token"), i18n.T("config.network.back")}
	values := map[string]string{
		options[0]: manageAdd,
		options[1]: manageDelete,
		options[2]: menuBack,
	}
	answer := ""
	if err := survey.AskOne(&survey.Select{Message: i18n.T("config.token.menu"), Options: options}, &answer, theme.SurveyOpts()...); err != nil {
		return "", handleSurveyError(err)
	}
	return values[answer], nil
}

func (a *App) runDeleteToken(ctx context.Context) error {
	chain, err := a.askTokenChain()
	if err != nil {
		return err
	}
	if chain == "" {
		return nil
	}
	tokens, err := config.LoadTokens(".")
	if err != nil {
		return err
	}
	chainTokens := tokens[chain]
	symbols := make([]string, 0, len(chainTokens))
	for symbol := range chainTokens {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	if len(symbols) == 0 {
		fmt.Println(theme.Info(i18n.T("config.delete_token.empty")))
		return nil
	}
	options := append([]string{}, symbols...)
	options = append(options, i18n.T("config.network.back"))
	choice := ""
	if err := survey.AskOne(&survey.Select{Message: i18n.T("config.delete_token.prompt"), Options: options}, &choice, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	if choice == i18n.T("config.network.back") {
		return nil
	}
	confirm := false
	if err := survey.AskOne(&survey.Confirm{Message: fmt.Sprintf(i18n.T("config.delete_token.confirm"), choice, chain), Default: false}, &confirm, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	if !confirm {
		return nil
	}
	if err := a.config.DeleteToken(ctx, chain, choice); err != nil {
		return err
	}
	if err := a.reloadRegistries(); err != nil {
		return err
	}
	fmt.Println(theme.Success(fmt.Sprintf(i18n.T("config.delete_token.saved"), choice, chain)))
	return nil
}

func (a *App) runAddToken(ctx context.Context) error {
	fmt.Println()
	fmt.Println(theme.Title(i18n.T("config.token.title")))
	fmt.Println(theme.Warning(i18n.T("config.token_warning")))
	chain, err := a.askTokenChain()
	if err != nil {
		return err
	}
	if chain == "" {
		return nil
	}
	token, err := a.resolveAndConfirmToken(ctx, chain)
	if err != nil {
		return err
	}
	if token == nil {
		return nil
	}
	fmt.Println(theme.Success(fmt.Sprintf(i18n.T("config.token.saved"), token.Symbol, chain)))
	if err := a.reloadRegistries(); err != nil {
		return err
	}
	return nil
}

func (a *App) askTokenChain() (string, error) {
	networks, err := config.LoadNetworks(".")
	if err != nil {
		return "", err
	}
	chains := make([]string, 0, len(networks))
	for slug := range networks {
		chains = append(chains, slug)
	}
	sort.Strings(chains)
	if len(chains) == 0 {
		return "", nil
	}
	chains = append(chains, i18n.T("config.network.back"))
	choice := ""
	if err := survey.AskOne(&survey.Select{Message: i18n.T("config.token.chain"), Options: chains}, &choice, theme.SurveyOpts()...); err != nil {
		return "", handleSurveyError(err)
	}
	if choice == i18n.T("config.network.back") {
		return "", nil
	}
	return choice, nil
}

func (a *App) resolveAndConfirmToken(ctx context.Context, chain string) (*config.Token, error) {
	address := ""
	message := i18n.T("config.token.address") + " (" + i18n.T("config.token.address.hint") + ")"
	if err := survey.AskOne(&survey.Input{Message: message}, &address, theme.SurveyOpts()...); err != nil {
		return nil, handleSurveyError(err)
	}
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return nil, nil
	}
	resolver := func(resolveCtx context.Context, resolveChain, resolveAddress string) (config.Token, error) {
		spin := render.StartSpinner(i18n.T("config.token.resolving"))
		line, resolveErr := a.transfer.ResolveToken(resolveCtx, resolveChain, resolveAddress)
		if resolveErr != nil {
			spin.Stop()
			return config.Token{}, resolveErr
		}
		spin.Success(i18n.T("transfer.checked"))
		return config.Token{Symbol: line.Symbol, Name: line.Name, Address: line.Address, Decimals: line.Decimals}, nil
	}
	token, err := a.config.ResolveToken(ctx, chain, trimmed, resolver)
	if err != nil {
		return nil, err
	}
	printTokenSummary(chain, token)
	confirm := false
	if err := survey.AskOne(&survey.Confirm{Message: i18n.T("config.token.confirm"), Default: false}, &confirm, theme.SurveyOpts()...); err != nil {
		return nil, handleSurveyError(err)
	}
	if !confirm {
		return nil, nil
	}
	if err := a.config.SaveToken(chain, token); err != nil {
		return nil, err
	}
	return &token, nil
}

func printTokenSummary(chain string, token config.Token) {
	fmt.Println()
	fmt.Println(theme.Title(i18n.T("config.token.summary")))
	fmt.Println(summaryRow(i18n.T("config.token.chain"), chain))
	fmt.Println(summaryRow(i18n.T("config.token.address"), token.Address))
	fmt.Println(summaryRow(i18n.T("config.token.name"), token.Name))
	fmt.Println(summaryRow(i18n.T("config.token.symbol"), token.Symbol))
	fmt.Println(summaryRow(i18n.T("config.token.decimals"), fmt.Sprintf("%d", token.Decimals)))
	fmt.Println(theme.Warning(i18n.T("config.token_warning")))
	fmt.Println()
}
