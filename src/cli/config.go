package cli

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/AlecAivazis/survey/v2"
	"golang.org/x/term"

	"multi-wallet-cli/app/configsvc"
	"multi-wallet-cli/cli/theme"
	"multi-wallet-cli/infra/config"
	"multi-wallet-cli/infra/crypto"
	"multi-wallet-cli/infra/i18n"
)

func (a *App) RunConfig(ctx context.Context) error {
	printTitleBanner()
	for {
		choice := ""
		err := survey.AskOne(&survey.Select{
			Message: i18n.T("config.menu"),
			Options: []string{i18n.T("config.language"), i18n.T("config.agent"), i18n.T("config.manage_networks"), i18n.T("config.manage_tokens"), i18n.T("config.back")},
		}, &choice, theme.SurveyOpts()...)
		if err := handleSurveyError(err); err != nil {
			return err
		}
		switch choice {
		case i18n.T("config.language"):
			if err := a.configureLanguage(ctx); err != nil {
				fmt.Println(theme.Error(err.Error()))
			}
		case i18n.T("config.agent"):
			if err := a.configureAgent(ctx); err != nil {
				fmt.Println(theme.Error(err.Error()))
			}
		case i18n.T("config.manage_networks"):
			if err := a.runManageNetworks(ctx); err != nil {
				fmt.Println(theme.Error(err.Error()))
			}
		case i18n.T("config.manage_tokens"):
			if err := a.runManageTokens(ctx); err != nil {
				fmt.Println(theme.Error(err.Error()))
			}
		default:
			return nil
		}
	}
}

// reloadRegistries refreshes the in-RAM network and token registries held by the
// transfer and balance services so a network or token added or removed in Config
// is reflected across the whole TUI without unlocking again.
func (a *App) reloadRegistries() error {
	if err := a.transfer.Reload(); err != nil {
		return err
	}
	if err := a.balance.Reload(); err != nil {
		return err
	}
	return nil
}

func (a *App) configureLanguage(ctx context.Context) error {
	choice := ""
	options := []string{i18n.T("config.english"), i18n.T("config.vietnamese")}
	if err := survey.AskOne(&survey.Select{Message: i18n.T("config.language"), Options: options}, &choice, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	language := "en"
	if choice == i18n.T("config.vietnamese") {
		language = "vi"
	}
	if err := a.config.SetLanguage(ctx, language); err != nil {
		return err
	}
	return nil
}

func (a *App) configureAgent(ctx context.Context) error {
	modelsByProvider := configsvc.ProviderModels()
	providers := make([]string, 0, len(modelsByProvider))
	for provider := range modelsByProvider {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	options := make([]string, 0, len(providers))
	providerByOption := make(map[string]string, len(providers))
	for _, provider := range providers {
		option := provider + " (" + modelsByProvider[provider] + ")"
		options = append(options, option)
		providerByOption[option] = provider
	}
	provider := ""
	if current := config.CurrentAgentConfig; current.Provider != "" {
		fmt.Println(theme.Info(i18n.T("config.provider.current", current.Provider, current.Model)))
	}
	if err := survey.AskOne(&survey.Select{Message: i18n.T("config.provider"), Options: options}, &provider, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	provider = providerByOption[provider]
	if provider == config.CurrentAgentConfig.Provider && config.CurrentAgentConfig.APIKey != "" {
		return a.manageCurrentAgent(ctx, provider, modelsByProvider[provider])
	}
	return a.changeAgentKey(ctx, provider, modelsByProvider[provider])
}

func (a *App) manageCurrentAgent(ctx context.Context, provider string, model string) error {
	choice := ""
	if err := survey.AskOne(&survey.Select{
		Message: i18n.T("config.provider.current", provider, model),
		Options: []string{i18n.T("config.provider.change"), i18n.T("config.provider.delete")},
	}, &choice, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	switch choice {
	case i18n.T("config.provider.change"):
		return a.changeAgentKey(ctx, provider, model)
	case i18n.T("config.provider.delete"):
		if err := a.config.DeleteAgentProvider(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) changeAgentKey(ctx context.Context, provider string, model string) error {
	fmt.Print(theme.Label(i18n.T("config.api_key") + ": "))
	apiKey, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return err
	}
	defer crypto.Zeroize(apiKey)
	if err := a.config.SetAgentProvider(ctx, provider, apiKey, a.master); err != nil {
		return err
	}
	return nil
}
