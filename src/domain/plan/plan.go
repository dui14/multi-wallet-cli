package plan

import (
	"fmt"
	"strings"
)

// Tool identifies one executable action inside a plan.
type Tool string

const (
	ToolTransfer Tool = "transfer"
	ToolSwap     Tool = "swap"
	ToolBridge   Tool = "bridge"
	ToolBalance  Tool = "balance"
)

// Action is one validated step the user confirms before execution.
type Action struct {
	Tool  Tool
	Args  map[string]string
	Valid bool
	Error string
}

// Plan is the ordered list of actions produced by menu or agent.
type Plan struct {
	Actions []Action
}

// Validate ensures every action names a known tool with non-empty args.
func (p Plan) Validate() error {
	if len(p.Actions) == 0 {
		return fmt.Errorf("plan has no actions")
	}
	for index, action := range p.Actions {
		switch action.Tool {
		case ToolTransfer, ToolSwap, ToolBridge, ToolBalance:
		default:
			return fmt.Errorf("action %d uses unknown tool %q", index, action.Tool)
		}
		if strings.TrimSpace(string(action.Tool)) == "" {
			return fmt.Errorf("action %d misses a tool name", index)
		}
	}
	return nil
}
