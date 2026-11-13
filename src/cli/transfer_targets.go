package cli

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/ethereum/go-ethereum/common"

	transferapp "multi-wallet-cli/app/transfer"
	"multi-wallet-cli/cli/theme"
	"multi-wallet-cli/domain/amount"
	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/i18n"
)

func parseAddressList(raw string) []string {
	replacer := strings.NewReplacer(",", "\n", ";", "\n", " ", "\n")
	parts := strings.Split(replacer.Replace(raw), "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func dedupeAddresses(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, strings.TrimSpace(value))
	}
	return out
}

func (a *App) askOneToManyDestinations(unlocked *wallet.WalletSet, request *transferapp.Request, vm string) error {
	for {
		options := a.destinationWalletOptions(unlocked)
		labels := make([]string, 0, len(options)+1)
		byLabel := make(map[string]string, len(options))
		for _, option := range options {
			labels = append(labels, option.label)
			byLabel[option.label] = option.address
		}
		manual := i18n.T("transfer.destination.manual")
		labels = append(labels, manual)
		selected := []string{}
		if err := survey.AskOne(&survey.MultiSelect{Message: i18n.T("transfer.recipients"), Options: labels}, &selected, theme.SurveyOpts()...); err != nil {
			return handleSurveyError(err)
		}
		chosen := make([]string, 0, len(selected))
		manualChosen := false
		for _, label := range selected {
			if label == manual {
				manualChosen = true
				continue
			}
			if address, ok := byLabel[label]; ok {
				chosen = append(chosen, address)
			}
		}
		pasted := ""
		if manualChosen {
			if err := survey.AskOne(&survey.Input{Message: i18n.T("transfer.paste_recipients")}, &pasted, theme.SurveyOpts()...); err != nil {
				return handleSurveyError(err)
			}
			if strings.TrimSpace(pasted) != "" {
				items, message, secret := inspectPastedAddresses(pasted, vm)
				if secret {
					fmt.Println(message)
					return errPasteSecurity
				}
				if message != "" {
					fmt.Println(message)
					continue
				}
				chosen = append(chosen, items...)
			}
		}
		request.Recipients = dedupeAddresses(chosen)
		if len(request.Recipients) == 0 {
			fmt.Println(theme.Error(i18n.T("transfer.destination.required")))
			continue
		}
		return a.askAmountMode(request)
	}
}

func (a *App) askAmountMode(request *transferapp.Request) error {
	options := []string{i18n.T("transfer.amount_equal"), i18n.T("transfer.amount_different")}
	choice := ""
	if err := survey.AskOne(&survey.Select{Message: i18n.T("transfer.amount_mode"), Options: options}, &choice, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	if choice == i18n.T("transfer.amount_different") {
		request.Split = transferapp.SplitPerRecipient
		return a.fillSplitEach(request)
	}
	request.Split = transferapp.SplitSame
	return a.fillEqualAmounts(request)
}

func (a *App) fillEqualAmounts(request *transferapp.Request) error {
	count := len(request.Recipients)
	for index := range request.Tokens {
		token := &request.Tokens[index]
		label := fmt.Sprintf(i18n.T("transfer.amount_for"), token.Symbol)
		value, blank, err := a.askBoundedAmount(token.Symbol, token.Decimals, token.Balance, count, label, true)
		if err != nil {
			return err
		}
		if blank {
			token.Max = true
			token.Amount = nil
			continue
		}
		token.Max = false
		token.Amount = value
	}
	return nil
}

func (a *App) fillSplitEach(request *transferapp.Request) error {
	for index := 0; index < len(request.Tokens); {
		token := &request.Tokens[index]
		token.PerRecipient = map[string]*big.Int{}
		values := make([]*big.Int, 0, len(request.Recipients))
		failed := false
		for _, recipient := range request.Recipients {
			label := fmt.Sprintf(i18n.T("transfer.each_for"), token.Symbol, theme.TruncateMiddle(recipient, 12))
			value, _, err := a.askBoundedAmount(token.Symbol, token.Decimals, token.Balance, 1, label, false)
			if err != nil {
				return err
			}
			token.PerRecipient[destinationKey(recipient)] = value
			values = append(values, value)
		}
		if err := amount.TotalLessThanBalance(values, token.Balance); err != nil {
			fmt.Println(theme.Error(amountRejectText(token.Symbol, token.Balance, token.Decimals, err)))
			failed = true
		}
		if failed {
			continue
		}
		index++
	}
	return nil
}

func destinationKey(value string) string {
	trimmed := strings.TrimSpace(value)
	if common.IsHexAddress(trimmed) {
		return common.HexToAddress(trimmed).Hex()
	}
	return strings.ToLower(trimmed)
}

func (a *App) askManyToOneAmounts(ctx context.Context, draft transferDraft, request *transferapp.Request) error {
	options := []string{i18n.T("transfer.sweep"), i18n.T("transfer.fixed"), i18n.T("transfer.keep")}
	choice := ""
	if err := survey.AskOne(&survey.Select{Message: i18n.T("transfer.mode_amount"), Options: options}, &choice, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	if choice == i18n.T("transfer.sweep") {
		return nil
	}
	keep := choice == i18n.T("transfer.keep")
	sources := request.Froms
	if len(sources) == 0 {
		sources = []wallet.PublicWallet{request.From}
	}
	for index := range request.Tokens {
		if request.Tokens[index].Native {
			continue
		}
		tight, err := a.tightestBalance(ctx, draft, sources, request.Tokens[index])
		if err != nil {
			return err
		}
		request.Tokens[index].Balance = tight
		label := fmt.Sprintf(i18n.T("transfer.amount_for"), request.Tokens[index].Symbol)
		value, blank, askErr := a.askBoundedAmount(request.Tokens[index].Symbol, request.Tokens[index].Decimals, tight, 1, label, !keep)
		if askErr != nil {
			return askErr
		}
		if keep {
			request.Tokens[index].Keep = value
			request.Tokens[index].Amount = nil
			request.Tokens[index].Max = false
			continue
		}
		if blank {
			request.Tokens[index].Max = true
			request.Tokens[index].Amount = nil
			continue
		}
		request.Tokens[index].Max = false
		request.Tokens[index].Amount = value
	}
	return nil
}
