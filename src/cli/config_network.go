package cli

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/AlecAivazis/survey/v2"

	"multi-wallet-cli/app/configsvc"
	"multi-wallet-cli/cli/render"
	"multi-wallet-cli/cli/theme"
	"multi-wallet-cli/infra/config"
	"multi-wallet-cli/infra/i18n"
)

type networkDraft struct {
	name     string
	chainID  string
	vmType   string
	symbol   string
	decimals string
	rpcs     []string
}

func (a *App) runManageNetworks(ctx context.Context) error {
	for {
		choice, err := a.askManageNetworksMenu()
		if err != nil {
			return err
		}
		switch choice {
		case manageAdd:
			if err := a.runAddNetwork(ctx); err != nil {
				return err
			}
		case manageDelete:
			if err := a.runDeleteNetwork(ctx); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func (a *App) askManageNetworksMenu() (string, error) {
	options := []string{i18n.T("config.add_network"), i18n.T("config.delete_network"), i18n.T("config.network.back")}
	values := map[string]string{
		options[0]: manageAdd,
		options[1]: manageDelete,
		options[2]: menuBack,
	}
	answer := ""
	if err := survey.AskOne(&survey.Select{Message: i18n.T("config.network.manage_menu"), Options: options}, &answer, theme.SurveyOpts()...); err != nil {
		return "", handleSurveyError(err)
	}
	return values[answer], nil
}

func (a *App) runDeleteNetwork(ctx context.Context) error {
	networks, err := config.LoadNetworks(".")
	if err != nil {
		return err
	}
	slugs := make([]string, 0, len(networks))
	for slug := range networks {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	if len(slugs) == 0 {
		fmt.Println(theme.Info(i18n.T("config.delete_network.empty")))
		return nil
	}
	options := append([]string{}, slugs...)
	options = append(options, i18n.T("config.network.back"))
	choice := ""
	if err := survey.AskOne(&survey.Select{Message: i18n.T("config.delete_network.prompt"), Options: options}, &choice, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	if choice == i18n.T("config.network.back") {
		return nil
	}
	confirm := false
	if err := survey.AskOne(&survey.Confirm{Message: fmt.Sprintf(i18n.T("config.delete_network.confirm"), choice), Default: false}, &confirm, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	if !confirm {
		return nil
	}
	if err := a.config.DeleteNetwork(ctx, choice); err != nil {
		return err
	}
	if err := a.reloadRegistries(); err != nil {
		return err
	}
	fmt.Println(theme.Success(fmt.Sprintf(i18n.T("config.delete_network.saved"), choice)))
	return nil
}

func (a *App) runAddNetwork(ctx context.Context) error {
	fmt.Println()
	fmt.Println(theme.Title(i18n.T("config.network.title")))
	fmt.Println(theme.Warning(i18n.T("config.network_warning")))
	draft := &networkDraft{}
	for {
		choice, err := a.askNetworkMenu(draft)
		if err != nil {
			return err
		}
		switch choice {
		case menuSave:
			return a.saveNetwork(ctx, draft)
		case menuBack:
			return nil
		case fieldName:
			if err := a.editNetworkText(i18n.T("config.network.name"), i18n.T("config.network.name.hint"), draft.name, true, &draft.name); err != nil {
				return err
			}
		case fieldChainID:
			if err := a.editNetworkText(i18n.T("config.network.chain_id"), i18n.T("config.network.chain_id.hint"), draft.chainID, false, &draft.chainID); err != nil {
				return err
			}
		case fieldVMType:
			if err := a.editNetworkVMType(draft); err != nil {
				return err
			}
		case fieldSymbol:
			if err := a.editNetworkText(i18n.T("config.network.symbol"), i18n.T("config.network.symbol.hint"), draft.symbol, true, &draft.symbol); err != nil {
				return err
			}
		case fieldDecimals:
			if err := a.editNetworkText(i18n.T("config.network.decimals"), i18n.T("config.network.decimals.hint"), draft.decimals, true, &draft.decimals); err != nil {
				return err
			}
		case fieldRPC:
			if err := a.manageNetworkRPCs(draft); err != nil {
				return err
			}
		}
	}
}

const (
	menuSave      = "__save__"
	menuBack      = "__back__"
	manageAdd     = "__manage_add__"
	manageDelete  = "__manage_delete__"
	fieldName     = "__name__"
	fieldChainID  = "__chainid__"
	fieldVMType   = "__vmtype__"
	fieldSymbol   = "__symbol__"
	fieldDecimals = "__decimals__"
	fieldRPC      = "__rpc__"
)

func (a *App) askNetworkMenu(draft *networkDraft) (string, error) {
	options := []string{
		fieldLabel(i18n.T("config.network.name"), draft.name),
		fieldLabel(i18n.T("config.network.chain_id"), draft.chainID),
		fieldLabel(i18n.T("config.network.vm_type"), draft.vmType),
		fieldLabel(i18n.T("config.network.symbol"), draft.symbol),
		fieldLabel(i18n.T("config.network.decimals"), draft.decimals),
		fieldLabel(i18n.T("config.network.rpc"), rpcSummary(draft.rpcs)),
		i18n.T("config.network.save"),
		i18n.T("config.network.back"),
	}
	values := map[string]string{
		options[0]: fieldName,
		options[1]: fieldChainID,
		options[2]: fieldVMType,
		options[3]: fieldSymbol,
		options[4]: fieldDecimals,
		options[5]: fieldRPC,
		options[6]: menuSave,
		options[7]: menuBack,
	}
	answer := ""
	if err := survey.AskOne(&survey.Select{Message: i18n.T("config.network.menu"), Options: options}, &answer, theme.SurveyOpts()...); err != nil {
		return "", handleSurveyError(err)
	}
	return values[answer], nil
}

func fieldLabel(label, value string) string {
	shown := strings.TrimSpace(value)
	if shown == "" {
		shown = "-"
	}
	return label + ": " + theme.Value(shown)
}

func rpcSummary(rpcs []string) string {
	if len(rpcs) == 0 {
		return i18n.T("config.network.rpc.none")
	}
	return strconv.Itoa(len(rpcs))
}

func (a *App) editNetworkText(label, hint, current string, required bool, target *string) error {
	message := label + " (" + hint + ")"
	answer := current
	opts := append([]survey.AskOpt{}, theme.SurveyOpts()...)
	if required {
		opts = append(opts, survey.WithValidator(survey.Required))
	}
	if err := survey.AskOne(&survey.Input{Message: message, Default: current, Help: theme.Help(hint)}, &answer, opts...); err != nil {
		return handleSurveyError(err)
	}
	*target = strings.TrimSpace(answer)
	return nil
}

func (a *App) editNetworkVMType(draft *networkDraft) error {
	options := []string{"evm", "svm", "tvm", "move"}
	answer := draft.vmType
	if answer == "" {
		answer = options[0]
	}
	if err := survey.AskOne(&survey.Select{Message: i18n.T("config.network.vm_type") + " (" + i18n.T("config.network.vm_type.hint") + ")", Options: options, Default: answer}, &answer, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	draft.vmType = answer
	return nil
}

func (a *App) manageNetworkRPCs(draft *networkDraft) error {
	for {
		options := []string{i18n.T("config.network.add_rpc")}
		if len(draft.rpcs) > 0 {
			options = append(options, i18n.T("config.network.remove_rpc"))
		}
		options = append(options, i18n.T("config.network.done"))
		choice := ""
		if err := survey.AskOne(&survey.Select{Message: i18n.T("config.network.manage") + " (" + rpcSummary(draft.rpcs) + ")", Options: options}, &choice, theme.SurveyOpts()...); err != nil {
			return handleSurveyError(err)
		}
		switch choice {
		case i18n.T("config.network.add_rpc"):
			if err := a.addNetworkRPC(draft); err != nil {
				return err
			}
		case i18n.T("config.network.remove_rpc"):
			if err := a.removeNetworkRPC(draft); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func (a *App) addNetworkRPC(draft *networkDraft) error {
	index := len(draft.rpcs) + 1
	message := fmt.Sprintf(i18n.T("config.network.rpc.entry"), index) + " (" + i18n.T("config.network.rpc.hint") + ")"
	answer := ""
	if err := survey.AskOne(&survey.Input{Message: message}, &answer, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		return nil
	}
	draft.rpcs = append(draft.rpcs, trimmed)
	return a.addNetworkRPC(draft)
}

func (a *App) removeNetworkRPC(draft *networkDraft) error {
	options := append([]string{}, draft.rpcs...)
	options = append(options, i18n.T("config.network.back"))
	choice := ""
	if err := survey.AskOne(&survey.Select{Message: i18n.T("config.network.remove_rpc"), Options: options}, &choice, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	if choice == i18n.T("config.network.back") {
		return nil
	}
	remaining := make([]string, 0, len(draft.rpcs))
	for _, rpc := range draft.rpcs {
		if rpc != choice {
			remaining = append(remaining, rpc)
		}
	}
	draft.rpcs = remaining
	return nil
}

func (a *App) saveNetwork(ctx context.Context, draft *networkDraft) error {
	if strings.TrimSpace(draft.name) == "" {
		fmt.Println(theme.Error(fmt.Sprintf(i18n.T("config.network.required"), i18n.T("config.network.name"))))
		return nil
	}
	if strings.TrimSpace(draft.vmType) == "" {
		fmt.Println(theme.Error(fmt.Sprintf(i18n.T("config.network.required"), i18n.T("config.network.vm_type"))))
		return nil
	}
	if strings.TrimSpace(draft.symbol) == "" {
		fmt.Println(theme.Error(fmt.Sprintf(i18n.T("config.network.required"), i18n.T("config.network.symbol"))))
		return nil
	}
	chainID, err := parseNetworkInt(draft.chainID, i18n.T("config.network.chain_id"))
	if err != nil {
		fmt.Println(theme.Error(err.Error()))
		return nil
	}
	decimals, err := parseNetworkDecimals(draft.decimals)
	if err != nil {
		fmt.Println(theme.Error(err.Error()))
		return nil
	}
	printNetworkSummary(draft)
	confirm := false
	if err := survey.AskOne(&survey.Confirm{Message: i18n.T("config.network.confirm"), Default: false}, &confirm, theme.SurveyOpts()...); err != nil {
		return handleSurveyError(err)
	}
	if !confirm {
		return nil
	}
	spin := render.StartSpinner(i18n.T("config.network.validating"))
	slug, err := a.config.AddNetwork(ctx, configsvc.NetworkDraft{
		Name:           draft.name,
		ChainID:        chainID,
		VMType:         draft.vmType,
		NativeSymbol:   draft.symbol,
		NativeDecimals: decimals,
		RPCs:           draft.rpcs,
	})
	if err != nil {
		spin.Error(err.Error())
		return err
	}
	spin.Success(fmt.Sprintf(i18n.T("config.network.saved"), slug))
	if err := a.reloadRegistries(); err != nil {
		return err
	}
	return nil
}

func parseNetworkInt(value, label string) (int64, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s %s", label, i18n.T("config.network.invalid"))
	}
	return parsed, nil
}

func parseNetworkDecimals(value string) (uint8, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, fmt.Errorf("%s %s", i18n.T("config.network.decimals"), i18n.T("config.network.required"))
	}
	parsed, err := strconv.ParseUint(trimmed, 10, 8)
	if err != nil || parsed > 36 {
		return 0, fmt.Errorf("%s %s", i18n.T("config.network.decimals"), i18n.T("config.network.invalid"))
	}
	return uint8(parsed), nil
}

func printNetworkSummary(draft *networkDraft) {
	fmt.Println()
	fmt.Println(theme.Title(i18n.T("config.network.summary")))
	fmt.Println(summaryRow(i18n.T("config.network.name"), draft.name))
	fmt.Println(summaryRow(i18n.T("config.network.chain_id"), draft.chainID))
	fmt.Println(summaryRow(i18n.T("config.network.vm_type"), draft.vmType))
	fmt.Println(summaryRow(i18n.T("config.network.symbol"), draft.symbol))
	fmt.Println(summaryRow(i18n.T("config.network.decimals"), draft.decimals))
	rpcValue := i18n.T("config.network.rpc.none")
	if len(draft.rpcs) > 0 {
		rpcValue = strings.Join(draft.rpcs, ", ")
	}
	fmt.Println(summaryRow(i18n.T("config.network.rpc"), rpcValue))
	fmt.Println()
}

func summaryRow(label, value string) string {
	shown := strings.TrimSpace(value)
	if shown == "" {
		shown = "-"
	}
	return "  " + theme.Label(label+": ") + theme.Value(shown)
}
