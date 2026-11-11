package cli

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/AlecAivazis/survey/v2"

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

func (a *App) askOneToManyRecipients(unlocked *wallet.WalletSet, request *transferapp.Request) error {
	options := a.destinationWalletOptions(unlocked)
	labels := make([]string, 0, len(options))
	byLabel := make(map[string]string, len(options))
	for _, option := range options {
		labels = append(labels, option.label)
		byLabel[option.label] = option.address
	}
	selected := []string{}
	if err := survey.AskOne(&survey.MultiSelect{Message: i18n.T("transfer.recipients"), Options: labels}, &selected, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	chosen := make([]string, 0, len(selected))
	for _, label := range selected {
		chosen = append(chosen, byLabel[label])
	}
	pasted := ""
	if err := survey.AskOne(&survey.Input{Message: i18n.T("transfer.paste_recipients")}, &pasted, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	request.Recipients = dedupeAddresses(append(chosen, parseAddressList(pasted)...))
	if len(request.Recipients) == 0 {
		return fmt.Errorf("select at least one recipient")
	}
	return a.askAmountMode(request)
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
	return nil
}

func (a *App) fillSplitEach(request *transferapp.Request) error {
	for index := range request.Tokens {
		token := request.Tokens[index]
		if token.Native {
			continue
		}
		token.PerRecipient = map[string]*big.Int{}
		for _, recipient := range request.Recipients {
			raw := ""
			label := fmt.Sprintf(i18n.T("transfer.each_for"), token.Symbol, theme.TruncateMiddle(recipient, 12))
			if err := survey.AskOne(&survey.Input{Message: label}, &raw, theme.SurveyOpts()...); err != nil {
				return handleSurveyError(err)
			}
			value, err := amount.Parse(strings.TrimSpace(raw), token.Decimals)
			if err != nil {
				return fmt.Errorf("%s: %w", token.Symbol, err)
			}
			token.PerRecipient[strings.ToLower(strings.TrimSpace(recipient))] = value
		}
		request.Tokens[index] = token
	}
	return nil
}

func (a *App) askManyToOneAmounts(request *transferapp.Request) error {
	options := []string{i18n.T("transfer.sweep"), i18n.T("transfer.fixed"), i18n.T("transfer.keep")}
	choice := ""
	if err := survey.AskOne(&survey.Select{Message: i18n.T("transfer.mode_amount"), Options: options}, &choice, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	if choice == i18n.T("transfer.sweep") {
		return nil
	}
	keep := choice == i18n.T("transfer.keep")
	for index := range request.Tokens {
		if request.Tokens[index].Native {
			continue
		}
		raw := ""
		label := fmt.Sprintf(i18n.T("transfer.amount_for"), request.Tokens[index].Symbol)
		if err := survey.AskOne(&survey.Input{Message: label}, &raw, theme.SurveyOpts()...); err != nil {
			return handleSurveyError(err)
		}
		value, err := amount.Parse(strings.TrimSpace(raw), request.Tokens[index].Decimals)
		if err != nil {
			return fmt.Errorf("%s: %w", request.Tokens[index].Symbol, err)
		}
		if keep {
			request.Tokens[index].Keep = value
		} else {
			request.Tokens[index].Amount = value
		}
	}
	return nil
}
