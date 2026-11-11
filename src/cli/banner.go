package cli

import (
	"fmt"

	"multi-wallet-cli/cli/theme"
	"multi-wallet-cli/infra/i18n"
)

func printTitleBanner() {
	fmt.Println()
	fmt.Println(theme.Label(i18n.T("app.banner.line")))
	fmt.Println(theme.Title(i18n.T("app.banner.title")))
	fmt.Println(theme.Body(i18n.T("app.banner.subtitle")))
	fmt.Println(theme.Label(i18n.T("app.banner.line")))
	fmt.Println()
}

func printFullBanner() {
	printTitleBanner()
	fmt.Println(theme.Body(i18n.T("app.banner.author")))
	fmt.Println(theme.Body(i18n.T("app.banner.github")))
	fmt.Println(theme.Body(i18n.T("app.banner.x")))
	fmt.Println(theme.Body(i18n.T("app.banner.feedback")))
	fmt.Println(theme.Label(i18n.T("app.banner.line")))
	fmt.Println()
}

func printUnlockExplain() {
	fmt.Println()
	fmt.Println(theme.Label(i18n.T("app.banner.line")))
	fmt.Println(theme.Title(i18n.T("unlock.explain.title")))
	fmt.Println(theme.Body(i18n.T("unlock.explain.step1")))
	fmt.Println(theme.Body(i18n.T("unlock.explain.step2")))
	fmt.Println()
	fmt.Println(theme.Warning(i18n.T("unlock.explain.security")))
	fmt.Println()
	fmt.Println(theme.Label(i18n.T("app.banner.line")))
	fmt.Println()
}
