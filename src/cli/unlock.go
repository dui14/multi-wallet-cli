package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"multi-wallet-cli/app/walletsvc"
	"multi-wallet-cli/cli/render"
	"multi-wallet-cli/cli/theme"
	"multi-wallet-cli/domain/errcodes"
	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/crypto"
	"multi-wallet-cli/infra/i18n"
	"multi-wallet-cli/infra/keyfile"
)

const maxUnlockAttempts = 5

func (a *App) RunUnlock(ctx context.Context) (*wallet.WalletSet, error) {
	hasWallets, err := a.wallets.HasWallets(ctx)
	if err != nil {
		return nil, err
	}
	if !hasWallets {
		printFullBanner()
		fmt.Println(theme.Body(i18n.T("unlock.empty")))
		fmt.Println(theme.Code(i18n.T("unlock.empty.action")))
		return nil, errcodes.ErrNoWallets
	}
	fileCount, err := a.wallets.WalletFileCount(ctx)
	if err != nil {
		return nil, err
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return nil, errors.New(i18n.T("error.non_tty"))
	}
	keyFile, err := keyfile.Load()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("keyfile.load.failed"), err)
	}
	defer crypto.Zeroize(keyFile)
	for attempt := 1; attempt <= maxUnlockAttempts; attempt++ {
		fmt.Println()
		fmt.Println(theme.Title(i18n.T("unlock.title")))
		fmt.Print(theme.Label(i18n.T("unlock.password") + ": "))
		password, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return nil, err
		}
		spin := render.StartSpinner(i18n.T("unlock.loading", fileCount))
		set, err := a.wallets.UnlockAll(ctx, password, keyFile)
		if err == nil {
			a.master = append([]byte(nil), password...)
		}
		crypto.Zeroize(password)
		if err == nil {
			spin.Success(i18n.T("unlock.success", set.Count()))
			failures := set.Failures()
			if len(failures) > 0 {
				names := make([]string, 0, len(failures))
				for _, failure := range failures {
					names = append(names, failure.File)
				}
				fmt.Println(theme.Warning(i18n.T("unlock.partial", strings.Join(names, ", "))))
			}
			return set, nil
		}
		spin.Error(i18n.T("unlock.attempt", attempt, maxUnlockAttempts))
		if !errors.Is(err, errcodes.ErrIncorrectPassword) {
			fmt.Println(theme.Error(walletsvc.UserMessage(err)))
		}
	}
	return nil, errcodes.ErrIncorrectPassword
}
