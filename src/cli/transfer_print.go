package cli

import (
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"

	transferapp "multi-wallet-cli/app/transfer"
	"multi-wallet-cli/cli/theme"
	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/i18n"
)

const defaultGasPriceGwei = 5

func printTransferSummary(draft transferDraft, plan transferapp.Plan) {
	fmt.Println()
	fmt.Println(theme.Title(i18n.T("transfer.summary")))
	fmt.Println(theme.Label(fmt.Sprintf("%s: %s · %d", i18n.T("transfer.queue"), draft.chain, len(plan.Lines))))
	for index, line := range plan.Lines {
		kind := line.Kind.Label()
		to := line.Recipient
		if to == "" {
			to = line.To
		}
		summary := fmt.Sprintf("%2d. %s · %s -> %s", index+1, kind, transferapp.FormatUnits(line.Value, line.Token.Decimals)+" "+line.Token.Symbol, theme.Code(to))
		fmt.Println("    " + summary)
	}
	gasNative := transferapp.FormatUnits(plan.Gas, draft.network.NativeDecimals)
	gasPriceGwei := float64(defaultGasPriceGwei)
	if v := os.Getenv("GAS_PRICE_GWEI"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			gasPriceGwei = n
		}
	}
	gasPriceWei := new(big.Int).Mul(big.NewInt(int64(gasPriceGwei)), big.NewInt(1000000000))
	costWei := new(big.Int).Mul(plan.Gas, gasPriceWei)
	decimals := big.NewInt(int64(draft.network.NativeDecimals))
	divisor := new(big.Int).Exp(big.NewInt(10), decimals, nil)
	costNative := new(big.Float).Quo(new(big.Float).SetInt(costWei), new(big.Float).SetInt(divisor))

	usdPart := ""
	if v := os.Getenv("NATIVE_USD_PRICE"); v != "" {
		if usdPrice, err := strconv.ParseFloat(v, 64); err == nil && usdPrice > 0 {
			costUsd, _ := costNative.Mul(costNative, big.NewFloat(usdPrice)).Float64()
			usdPart = i18n.T("transfer.est_gas_usd", costUsd)
		}
	}
	fmt.Println(theme.Label(i18n.T("transfer.est_gas") + ": " + theme.Value(gasNative+" "+draft.network.NativeSymbol+usdPart)))
	for _, summary := range plan.Summaries {
		fmt.Println(theme.Label(i18n.T("transfer.batch", summary.Wallet, summary.Calls, summary.Chunks, batchPathLabel(summary.Path))))
	}
	for _, warning := range plan.Warnings {
		fmt.Println(theme.Warning(warning))
	}
	warnedFee := false
	for _, phase := range plan.Phases {
		if warnedFee || (!phase.FeeOnTransfer && !phase.Token.Custom && !phase.Token.Unverified) {
			continue
		}
		fmt.Println(theme.Warning(i18n.T("transfer.fee_warning")))
		warnedFee = true
	}
	if len(plan.Skipped) > 0 {
		fmt.Println(theme.Label(i18n.T("transfer.skipped")))
		for _, skip := range plan.Skipped {
			fmt.Println("    " + theme.Label(fmt.Sprintf(i18n.T("transfer.skipped_empty"), skip.Wallet, skip.Token, skip.Reason)))
		}
	}
	fmt.Println()
}

func printTransferReport(draft transferDraft, report transferapp.Report, labels map[string]string) {
	fmt.Println()
	for _, owner := range report.Results {
		sent := 0
		for _, line := range owner.Lines {
			if line.Err == nil {
				sent++
			}
		}
		fmt.Println(theme.Title(fmt.Sprintf(i18n.T("transfer.result_wallet")+" "+i18n.T("transfer.result_done"), owner.Wallet, sent, len(owner.Lines))))
		for _, line := range owner.Lines {
			label := line.Kind.Label()
			switch {
			case line.Err == nil:
				link := draft.network.Explorer + "tx/" + line.TxHash
				if line.Kind == transferapp.KindTransfer {
					recipient := recipientLabel(line.Recipient, labels)
					fmt.Println("    " + theme.Success(fmt.Sprintf(i18n.T("transfer.line_sent_to"), owner.Wallet, recipient, line.Label(), theme.Code(link))))
					continue
				}
				fmt.Println("    " + theme.Success(fmt.Sprintf(i18n.T("transfer.line_sent"), label, line.Label(), theme.Code(link))))
			case line.TxHash != "":
				link := draft.network.Explorer + "tx/" + line.TxHash
				fmt.Println("    " + theme.Pending(fmt.Sprintf(i18n.T("transfer.line_pending"), label, line.Label(), theme.Code(link)+" · "+line.Err.Error())))
			case line.Status == 0:
				fmt.Println("    " + theme.Error(fmt.Sprintf(i18n.T("transfer.line_failed"), label, line.Label(), line.Err.Error())))
			default:
				fmt.Println("    " + theme.Pending(fmt.Sprintf(i18n.T("transfer.line_pending"), label, line.Label(), line.Err.Error())))
			}
		}
	}
	fmt.Println()
}

// recipientLabel resolves a recipient address to a saved wallet label when it
// matches an unlocked wallet, otherwise shows a truncated address.
func batchPathLabel(path string) string {
	switch path {
	case "set-code":
		return i18n.T("transfer.path_setcode")
	case "delegated":
		return i18n.T("transfer.path_delegated")
	case "mixed":
		return i18n.T("transfer.path_mixed")
	default:
		return i18n.T("transfer.path_standard")
	}
}

func recipientLabel(address string, labels map[string]string) string {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return "-"
	}
	if label, ok := labels[strings.ToLower(trimmed)]; ok {
		return label
	}
	return theme.TruncateMiddle(trimmed, 14)
}

// walletLabelMap indexes unlocked EVM wallet addresses to their labels so a
// transfer report can name a saved destination wallet instead of a raw address.
func walletLabelMap(unlocked *wallet.WalletSet) map[string]string {
	labels := map[string]string{}
	if unlocked == nil {
		return labels
	}
	for _, item := range unlocked.Items() {
		if item.ChainKind != wallet.ChainKindEVM || item.Label == "" {
			continue
		}
		labels[strings.ToLower(item.Address)] = item.Label
	}
	return labels
}
