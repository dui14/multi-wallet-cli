package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/AlecAivazis/survey/v2"
	"golang.org/x/term"

	"multi-wallet-cli/app/walletsvc"
	"multi-wallet-cli/cli/render"
	"multi-wallet-cli/cli/theme"
	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/crypto"
	"multi-wallet-cli/infra/i18n"
	"multi-wallet-cli/infra/keyfile"
)

func (a *App) RunSetup(ctx context.Context) error {
	printTitleBanner()
	printUnlockExplain()
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New(i18n.T("error.non_tty"))
	}
	keyFile, created, err := keyfile.Ensure()
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("keyfile.ensure.failed"), err)
	}
	defer crypto.Zeroize(keyFile)
	if created {
		path, _ := keyfile.Path()
		fmt.Println(theme.Success(i18n.T("keyfile.created", path)))
	}
	hasWallets, err := a.wallets.HasWallets(ctx)
	if err != nil {
		return err
	}
	password, err := a.setupPassword(ctx, hasWallets)
	if err != nil {
		return err
	}
	defer crypto.Zeroize(password)
	if hasWallets {
		count, err := a.wallets.WalletFileCount(ctx)
		if err != nil {
			return err
		}
		spin := render.StartSpinner(i18n.T("unlock.loading", count))
		set, err := a.wallets.UnlockAll(ctx, password, keyFile)
		if err != nil {
			spin.Error(walletsvc.UserMessage(err))
			return err
		}
		spin.Success(i18n.T("unlock.success", set.Count()))
		for _, item := range set.Items() {
			fmt.Println(theme.Info(i18n.T("setup.wallet.existing", item.Label, item.ChainKind, theme.TruncateMiddle(item.Address, 18))))
		}
	}
	added := 0
	for {
		if added == 0 {
			addWallet := true
			err := survey.AskOne(&survey.Confirm{
				Message: i18n.T("setup.wallet.start"),
				Default: true,
			}, &addWallet, theme.SurveyOpts()...)
			if err := handleSurveyError(err); err != nil {
				return err
			}
			if !addWallet {
				break
			}
		}
		result, err := a.setupWallet(ctx, password, keyFile)
		if err != nil {
			if err.Error() == "interrupt" {
				return handleSurveyError(err)
			}
			fmt.Println(theme.Error(walletsvc.UserMessage(err)))
			continue
		}
		added++
		fmt.Println(theme.Success(i18n.T("setup.wallet.saved", result.Label, theme.TruncateMiddle(result.Address, 18), result.File)))
		addAnother := false
		err = survey.AskOne(&survey.Confirm{
			Message: i18n.T("setup.wallet.next"),
			Default: true,
		}, &addAnother, theme.SurveyOpts()...)
		if err := handleSurveyError(err); err != nil {
			return err
		}
		if !addAnother {
			break
		}
	}
	if added == 0 {
		fmt.Println(theme.Info(i18n.T("setup.summary.empty")))
		return nil
	}
	fmt.Println(theme.Success(i18n.T("setup.summary.success", added)))
	return nil
}

func (a *App) setupPassword(ctx context.Context, hasWallets bool) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fmt.Println()
	fmt.Println(theme.Title(i18n.T("setup.title")))
	if hasWallets {
		fmt.Print(theme.Label(i18n.T("setup.master.existing") + ": "))
		password, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return nil, err
		}
		return password, nil
	}
	for {
		fmt.Println(theme.Label(i18n.T("setup.master.create")))
		fmt.Print(theme.Label(i18n.T("unlock.password") + ": "))
		first, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return nil, err
		}
		if err := walletsvc.ValidateMasterPassword(first); err != nil {
			fmt.Println(theme.Error(walletsvc.UserMessage(err)))
			crypto.Zeroize(first)
			continue
		}
		fmt.Print(theme.Label(i18n.T("setup.master.confirm") + ": "))
		second, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			crypto.Zeroize(first)
			return nil, err
		}
		if !bytes.Equal(first, second) {
			fmt.Println(theme.Error(i18n.T("setup.password.mismatch")))
			crypto.Zeroize(first)
			crypto.Zeroize(second)
			continue
		}
		crypto.Zeroize(second)
		return first, nil
	}
}

func (a *App) setupWallet(ctx context.Context, password []byte, keyFile []byte) (walletsvc.ImportWalletResult, error) {
	if err := ctx.Err(); err != nil {
		return walletsvc.ImportWalletResult{}, err
	}
	fmt.Println()
	fmt.Println(theme.Title(i18n.T("setup.title")))
	fmt.Print(theme.Label(i18n.T("setup.wallet.private_key") + ": "))
	privateKey, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return walletsvc.ImportWalletResult{}, err
	}
	defer crypto.Zeroize(privateKey)

	detectedKind, detectedAddress, detectErr := walletsvc.DetectPrivateKeyKind(privateKey)
	if detectErr == nil {
		fmt.Println(theme.Info(fmt.Sprintf("%s · %s", detectedKind, theme.TruncateMiddle(detectedAddress, 18))))
	}
	kind := detectedKind
	if detectErr != nil {
		kind, err = askWalletKind("")
		if err != nil {
			return walletsvc.ImportWalletResult{}, err
		}
	}
	label := ""
	err = survey.AskOne(&survey.Input{
		Message: i18n.T("setup.wallet.label"),
	}, &label, theme.SurveyOpts()...)
	if err := handleSurveyError(err); err != nil {
		return walletsvc.ImportWalletResult{}, err
	}
	spin := render.StartSpinner(i18n.T("setup.wallet.encrypting"))
	result, err := a.wallets.ImportWallet(ctx, walletsvc.ImportWalletRequest{
		PrivateKey: privateKey,
		ChainKind:  kind,
		Label:      label,
		Password:   password,
		KeyFile:    keyFile,
	})
	if err != nil {
		spin.Error(walletsvc.UserMessage(err))
		return walletsvc.ImportWalletResult{}, err
	}
	spin.Success(i18n.T("state.success"))
	return result, nil
}

func askWalletKind(detected wallet.ChainKind) (wallet.ChainKind, error) {
	options := []string{i18n.T("setup.wallet.kind.detect"), i18n.T("setup.wallet.kind.evm"), i18n.T("setup.wallet.kind.solana")}
	defaultIndex := 0
	if detected == "" {
		defaultIndex = 1
	}
	answer := ""
	err := survey.AskOne(&survey.Select{
		Message: i18n.T("setup.wallet.kind"),
		Options: options,
		Default: options[defaultIndex],
	}, &answer, theme.SurveyOpts()...)
	if err := handleSurveyError(err); err != nil {
		return "", err
	}
	switch answer {
	case i18n.T("setup.wallet.kind.detect"):
		return detected, nil
	case i18n.T("setup.wallet.kind.evm"):
		return wallet.ChainKindEVM, nil
	case i18n.T("setup.wallet.kind.solana"):
		return wallet.ChainKindSolana, nil
	default:
		return detected, nil
	}
}
