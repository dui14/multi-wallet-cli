package cli

import (
	"context"
	"fmt"

	"github.com/AlecAivazis/survey/v2"

	transferapp "multi-wallet-cli/app/transfer"
	"multi-wallet-cli/cli/render"
	"multi-wallet-cli/cli/theme"
	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/i18n"
)

func (a *App) planTransfer(ctx context.Context, draft transferDraft, request transferapp.Request, signer transferapp.KeySigner) (transferapp.Plan, error) {

	spin := render.StartSpinner(i18n.T("transfer.check"))
	var plan transferapp.Plan
	var err error
	switch draft.mode {
	case i18n.T("transfer.many2one"):
		plan, err = a.transfer.PlanManyToOne(ctx, request, signer)
	case i18n.T("transfer.one2many"):
		plan, err = a.transfer.PlanOneToMany(ctx, request, signer)
	default:
		plan, err = a.transfer.PlanOneToOne(ctx, request, signer)
	}
	if err != nil {
		spin.Error(err.Error())
		return plan, err
	}
	spin.Success(i18n.T("transfer.checked"))
	printTransferSummary(draft, plan)
	return plan, nil
}

func (a *App) confirmAndExecute(ctx context.Context, draft transferDraft, plan transferapp.Plan, unlocked *wallet.WalletSet) error {
	options := []string{i18n.T("transfer.execute"), i18n.T("transfer.edit"), i18n.T("transfer.cancel")}
	choice := options[0]
	if err := survey.AskOne(&survey.Select{Message: i18n.T("transfer.action"), Options: options}, &choice, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	switch choice {
	case i18n.T("transfer.cancel"):
		return fmt.Errorf("transfer canceled")
	case i18n.T("transfer.edit"):
		return a.RunTransfer(ctx, unlocked)
	}
	signer := transferKeySigner(unlocked)
	labels := walletLabelMap(unlocked)
	spin := render.StartSpinner(i18n.T("transfer.executing"))
	report, err := a.transfer.Execute(ctx, plan, signer)
	spin.Stop()
	printTransferReport(draft, report, labels)
	if err != nil {
		return err
	}
	if len(report.Failed) > 0 {
		retry := false
		if askErr := survey.AskOne(&survey.Confirm{Message: i18n.T("transfer.retry"), Default: false}, &retry, theme.SurveyOpts()...); askErr != nil {
			return handleSurveyError(askErr)
		}
		if retry {
			spin := render.StartSpinner(i18n.T("transfer.executing"))
			retryReport, retryErr := a.transfer.Retry(ctx, plan, report.Failed, signer)
			spin.Stop()
			printTransferReport(draft, retryReport, labels)
			return retryErr
		}
	}
	return nil
}

func transferKeySigner(unlocked *wallet.WalletSet) transferapp.KeySigner {
	return func(label string) ([]byte, bool) {
		entry := unlocked.ByLabel(label)
		if entry == nil {
			return nil, false
		}
		key := entry.PrivateKeyCopy()
		if len(key) == 0 {
			return nil, false
		}
		return key, true
	}
}
