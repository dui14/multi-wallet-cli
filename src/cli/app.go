package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	balanceapp "multi-wallet-cli/app/balance"
	bridgeapp "multi-wallet-cli/app/bridge"
	"multi-wallet-cli/app/configsvc"
	"multi-wallet-cli/app/relayexec"
	swapapp "multi-wallet-cli/app/swap"
	transferapp "multi-wallet-cli/app/transfer"
	"multi-wallet-cli/app/walletsvc"
	"multi-wallet-cli/cli/theme"
	"multi-wallet-cli/domain/errcodes"
	"multi-wallet-cli/infra/aggregator/relay"
	"multi-wallet-cli/infra/crypto"
	"multi-wallet-cli/infra/evm"
	"multi-wallet-cli/infra/i18n"
	"multi-wallet-cli/infra/keystore"
	"multi-wallet-cli/infra/logcsv"
)

type Options struct {
	Setup bool
}

type App struct {
	wallets  *walletsvc.Service
	config   *configsvc.Service
	balance  *balanceapp.Service
	transfer *transferapp.Service
	swap     *swapapp.Service
	bridge   *bridgeapp.Service
	signer   *keySignerHolder
	txLogger *logcsv.Writer
	master   []byte
}

func Run(ctx context.Context, opts Options) error {
	theme.Init()
	repo := keystore.NewRepository(".")
	balanceService, balanceErr := balanceapp.New(".")
	if balanceErr != nil {
		return balanceErr
	}
	transferService, transferErr := transferapp.NewService(".")
	if transferErr != nil {
		return transferErr
	}
	txLogger, loggerErr := logcsv.NewWriter(filepath.Join(".", "logs"))
	if loggerErr != nil {
		return loggerErr
	}
	transferService.AttachLogger(txLogger)
	networks, tokens, _ := loadSwapRegistries(".")
	signer := &keySignerHolder{}
	relayClient := relay.NewClient(".")
	relayEngine := relayexec.NewEngine(relayClient, evm.NewClient(), signer.sign, chainResolver(networks))
	swapService := swapapp.NewService(networks, tokens, relayEngine, txLogger)
	bridgeService := bridgeapp.NewService(networks, tokens, relayEngine, txLogger)
	app := &App{wallets: walletsvc.New(repo), config: configsvc.New(repo, "."), balance: balanceService, transfer: transferService, swap: swapService, bridge: bridgeService, signer: signer, txLogger: txLogger}
	defer app.wallets.Zeroize()
	defer func() { crypto.Zeroize(app.master) }()
	defer txLogger.Close()
	if language, err := repo.LoadLanguage(ctx); err == nil {
		i18n.SetLanguage(language)
	}

	if opts.Setup {
		return app.RunSetup(ctx)
	}
	unlocked, err := app.RunUnlock(ctx)
	if err != nil {
		if errors.Is(err, errcodes.ErrNoWallets) {
			return nil
		}
		return err
	}
	app.signer.set(unlocked)
	return app.RunMenu(ctx, unlocked)
}

func handleSurveyError(err error) error {
	if err == nil {
		return nil
	}
	if err.Error() == "interrupt" {
		return fmt.Errorf("%w: %s", errcodes.ErrInterrupted, i18n.T("error.interrupted"))
	}
	return err
}
