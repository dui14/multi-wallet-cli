package transfer

import (
	"math/big"
	"strings"

	"multi-wallet-cli/domain/wallet"
)

// Transfer mode identifiers persisted in the transaction log.
const (
	ModeOneToOne  = "transfer_1to1"
	ModeOneToMany = "transfer_1tomany"
	ModeManyToOne = "transfer_manyto1"
	ModeSwap      = "swap"
	ModeBridge    = "bridge"
)

// Kind classifies each line in a transfer queue.
type Kind int

const (
	KindTransfer Kind = iota
)

// Label renders a human-friendly action name for a line kind.
func (k Kind) Label() string {
	return "transfer"
}

// SplitMode selects how a token is divided across many recipients.
type SplitMode int

const (
	SplitSame SplitMode = iota
	SplitPerRecipient
)

// AmountMode selects how a token amount is derived.
type AmountMode int

const (
	AmountFixed AmountMode = iota
	AmountMax
	AmountKeep
)

// TokenLine is one token selected for transfer.
type TokenLine struct {
	Symbol       string
	Name         string
	Address      string
	Decimals     uint8
	Native       bool
	Custom       bool
	Unverified   bool
	Amount       *big.Int
	Max          bool
	Keep         *big.Int
	Balance      *big.Int
	PerRecipient map[string]*big.Int
}

// Request is the wizard draft shared by all three modes.
type Request struct {
	Mode       string
	Chain      string
	From       wallet.PublicWallet
	Froms      []wallet.PublicWallet
	Tokens     []TokenLine
	To         string
	Recipients []string
	Split      SplitMode
	Reserve    *big.Int
}

// TokenPhase is the planned queue for one token inside one plan.
type TokenPhase struct {
	Token         TokenLine
	Recipients    []string
	Values        []*big.Int
	Sent          int
	Skipped       bool
	Reason        string
	Fee           *big.Int
	GasUnits      uint64
	FeeOnTransfer bool
}

// LinePlan is one broadcast step with ordering and gas metadata.
type LinePlan struct {
	Kind      Kind
	Owner     wallet.PublicWallet
	Token     TokenLine
	Recipient string
	To        string
	Value     *big.Int
	Data      []byte
	Nonce     uint64
	GasLimit  uint64
	Fee       *big.Int
	DependsOn []int
	Path      BatchPath
	Chunk     int
}

// BatchPath is how one owner chunk will be signed.
type BatchPath int

const (
	PathOrdinary BatchPath = iota + 1
	PathSetCode
	PathSelfCall
)

// Label is the summary token for a batch path.
func (p BatchPath) Label() string {
	switch p {
	case PathSetCode:
		return "set-code"
	case PathSelfCall:
		return "delegated"
	default:
		return "standard"
	}
}

// OwnerSummary is the confirm-screen description of one owner's batch.
type OwnerSummary struct {
	Wallet string
	Calls  int
	Chunks int
	Path   string
	Gas    *big.Int
}

// Plan is the validated, ordered transfer ready for confirmation.
type Plan struct {
	Request   Request
	Lines     []LinePlan
	Phases    []TokenPhase
	Skipped   []SkipRow
	Total     *big.Int
	Gas       *big.Int
	Warnings  []string
	Summaries []OwnerSummary
}

// SkipRow records why one wallet was excluded from a many-to-one plan.
type SkipRow struct {
	Wallet string
	Token  string
	Reason string
}

// LineResult is the outcome of one executed line.
type LineResult struct {
	Kind      Kind
	Token     string
	Amount    *big.Int
	Decimals  uint8
	Recipient string
	TxHash    string
	Status    int
	Block     *big.Int
	Err       error
}

// Label renders "amount symbol" for a transfer, or "approve" otherwise.
func (r LineResult) Label() string {
	if r.Kind != KindTransfer {
		return r.Kind.Label()
	}
	if r.Amount == nil {
		return r.Token
	}
	return FormatUnits(r.Amount, r.Decimals) + " " + r.Token
}

// FormatUnits renders base units with decimals as a decimal string.
func FormatUnits(raw *big.Int, decimals uint8) string {
	if raw == nil {
		return "0"
	}
	if decimals == 0 {
		return raw.String()
	}
	quotient := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	whole := new(big.Int).Div(raw, quotient)
	fraction := new(big.Int).Mod(raw, quotient)
	padded := fraction.String()
	for len(padded) < int(decimals) {
		padded = "0" + padded
	}
	trimmed := strings.TrimRight(padded, "0")
	if trimmed == "" {
		return whole.String()
	}
	return whole.String() + "." + trimmed
}

// OwnerResult groups executed lines by sending wallet.
type OwnerResult struct {
	Wallet string
	Lines  []LineResult
}

// Report is the aggregate execution outcome.
type Report struct {
	Results []OwnerResult
	Failed  []LinePlan
}
