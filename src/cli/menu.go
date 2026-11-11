package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/AlecAivazis/survey/v2"

	"multi-wallet-cli/app/walletsvc"
	"multi-wallet-cli/cli/render"
	"multi-wallet-cli/cli/theme"
	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/i18n"
)

func (a *App) RunMenu(ctx context.Context, wallets *wallet.WalletSet) error {
	count := 0
	if wallets != nil {
		count = wallets.Count()
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		printTitleBanner()
		fmt.Println(theme.Label(i18n.T("menu.status", count, strings.ToUpper(i18n.Language()))))
		if count == 0 {
			fmt.Println(theme.Warning(i18n.T("unlock.empty.action")))
		}
		fmt.Println(theme.Help(i18n.T("menu.help")))
		options := []string{
			i18n.T("menu.transfer"), i18n.T("menu.swap"), i18n.T("menu.bridge"),
			i18n.T("menu.balance"), i18n.T("menu.agent"), i18n.T("menu.config"), i18n.T("menu.exit"),
		}
		answer := ""
		err := survey.AskOne(&survey.Select{
			Message: i18n.T("app.title"),
			Options: options,
		}, &answer, theme.SurveyOpts()...)
		if err := handleSurveyError(err); err != nil {
			return err
		}
		if answer == i18n.T("menu.exit") {
			a.wallets.Zeroize()
			fmt.Println(theme.Success(i18n.T("state.success")))
			return nil
		}
		if answer == i18n.T("menu.config") {
			if err := a.RunConfig(ctx); err != nil {
				fmt.Println(theme.Error(walletsvc.UserMessage(err)))
			}
			continue
		}
		if answer == i18n.T("menu.balance") {
			if err := a.RunBalance(ctx, wallets); err != nil {
				fmt.Println(theme.Error(err.Error()))
			}
			continue
		}
		if answer == i18n.T("menu.transfer") {
			if err := a.RunTransfer(ctx, wallets); err != nil {
				fmt.Println(theme.Error(err.Error()))
			}
			continue
		}
		if answer == i18n.T("menu.swap") {
			if err := a.RunSwap(ctx, wallets); err != nil {
				fmt.Println(theme.Error(err.Error()))
			}
			continue
		}
		if answer == i18n.T("menu.bridge") {
			if err := a.RunBridge(ctx, wallets); err != nil {
				fmt.Println(theme.Error(err.Error()))
			}
			continue
		}
		spin := render.StartSpinner(i18n.T("menu.loading", answer))
		spin.Success(i18n.T("menu.unimplemented", answer))
	}
}
