package cli

import (
	"context"
	"sort"

	"github.com/AlecAivazis/survey/v2"

	transferapp "multi-wallet-cli/app/transfer"
	"multi-wallet-cli/cli/theme"
	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/config"
	"multi-wallet-cli/infra/i18n"
)

type transferDraft struct {
	mode    string
	chain   string
	network config.Network
}

func (a *App) RunTransfer(ctx context.Context, unlocked *wallet.WalletSet) error {
	printTitleBanner()
	if unlocked == nil || unlocked.Count() == 0 {
		return errNoWallets
	}
	if a.transfer == nil {
		return errTransferUnavailable
	}
	draft, err := a.newTransferDraft()
	if err != nil {
		return err
	}
	request, err := a.askTransferForm(ctx, unlocked, draft)
	if err != nil {
		return err
	}
	plan, err := a.planTransfer(ctx, draft, request, transferKeySigner(unlocked))
	if err != nil {
		return err
	}
	return a.confirmAndExecute(ctx, draft, plan, unlocked)
}

func (a *App) newTransferDraft() (transferDraft, error) {
	mode, err := a.askTransferMode()
	if err != nil {
		return transferDraft{}, err
	}
	chain, err := a.askTransferChain()
	if err != nil {
		return transferDraft{}, err
	}
	network, ok := a.transfer.Networks()[chain]
	if !ok {
		return transferDraft{}, errChainNotFound
	}
	return transferDraft{mode: mode, chain: chain, network: network}, nil
}

func (a *App) askTransferMode() (string, error) {
	options := []string{i18n.T("transfer.one2one"), i18n.T("transfer.one2many"), i18n.T("transfer.many2one")}
	mode := ""
	if err := survey.AskOne(&survey.Select{Message: i18n.T("transfer.mode"), Options: options}, &mode, theme.SurveyOpts()...); err != nil {
		return "", handleSurveyError(err)
	}
	return mode, nil
}

func (a *App) askTransferChain() (string, error) {
	options := a.evmChainOptions()
	chain := ""
	if err := survey.AskOne(&survey.Select{Message: i18n.T("transfer.chain"), Options: options}, &chain, theme.SurveyOpts()...); err != nil {
		return "", handleSurveyError(err)
	}
	return chain, nil
}

func (a *App) evmChainOptions() []string {
	options := make([]string, 0)
	for slug, network := range a.transfer.Networks() {
		if network.VMType == "evm" {
			options = append(options, slug)
		}
	}
	sort.Strings(options)
	return options
}

func (a *App) askTransferForm(ctx context.Context, unlocked *wallet.WalletSet, draft transferDraft) (transferapp.Request, error) {
	request := transferapp.Request{Chain: draft.chain}
	switch draft.mode {
	case i18n.T("transfer.many2one"):
		request.Mode = transferapp.ModeManyToOne
		return a.askManyToOneForm(ctx, unlocked, draft, request)
	case i18n.T("transfer.one2many"):
		request.Mode = transferapp.ModeOneToMany
		return a.askOneToManyForm(ctx, unlocked, draft, request)
	default:
		request.Mode = transferapp.ModeOneToOne
		return a.askOneToOneForm(ctx, unlocked, draft, request)
	}
}

func (a *App) askOneToOneForm(ctx context.Context, unlocked *wallet.WalletSet, draft transferDraft, request transferapp.Request) (transferapp.Request, error) {
	owner, err := a.askTransferSource(unlocked)
	if err != nil {
		return request, err
	}
	request.From = owner
	tokens, err := a.askTransferTokens(ctx, draft, owner)
	if err != nil {
		return request, err
	}
	request.Tokens = tokens
	to, err := a.askDestinationSingle(unlocked)
	if err != nil {
		return request, err
	}
	request.To = to
	return request, nil
}

func (a *App) askOneToManyForm(ctx context.Context, unlocked *wallet.WalletSet, draft transferDraft, request transferapp.Request) (transferapp.Request, error) {
	owner, err := a.askTransferSource(unlocked)
	if err != nil {
		return request, err
	}
	request.From = owner
	tokens, err := a.askTransferTokens(ctx, draft, owner)
	if err != nil {
		return request, err
	}
	request.Tokens = tokens
	if err := a.askOneToManyRecipients(unlocked, &request); err != nil {
		return request, err
	}
	return request, nil
}

func (a *App) askManyToOneForm(ctx context.Context, unlocked *wallet.WalletSet, draft transferDraft, request transferapp.Request) (transferapp.Request, error) {
	sources, err := a.askTransferSources(unlocked)
	if err != nil {
		return request, err
	}
	request.From = sources[0]
	request.Froms = sources
	allTokens, err := a.askTransferTokens(ctx, draft, sources[0])
	if err != nil {
		return request, err
	}
	request.Tokens = allTokens
	if err := a.askManyToOneAmounts(&request); err != nil {
		return request, err
	}
	to, err := a.askDestinationSingle(unlocked)
	if err != nil {
		return request, err
	}
	request.To = to
	return request, nil
}
