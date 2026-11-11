package logcsv

import "time"

// Status is the on-chain lifecycle state recorded for one logged transaction.
type Status string

const (
	StatusSkipped         Status = "skipped"
	StatusSubmitted       Status = "submitted"
	StatusSuccess         Status = "success"
	StatusReverted        Status = "reverted"
	StatusDropped         Status = "dropped"
	StatusReplaced        Status = "replaced"
	StatusBridgePending   Status = "bridge_pending"
	StatusBridgeCompleted Status = "bridge_completed"
	StatusBridgeRefunded  Status = "bridge_refunded"
)

// Normalized error codes written to the error column. Raw RPC or provider
// messages are never persisted; only these stable codes are recorded.
const (
	ErrReverted          = "REVERTED"
	ErrInsufficientFunds = "INSUFFICIENT_FUNDS"
	ErrRPCTimeout        = "RPC_TIMEOUT"
	ErrQuoteExpired      = "QUOTE_EXPIRED"
	ErrSigningFailed     = "SIGNING_FAILED"
	ErrBroadcastFailed   = "BROADCAST_FAILED"
	ErrDropped           = "DROPPED"
	ErrReplaced          = "REPLACED"
	ErrNoRoute           = "NO_ROUTE"
	ErrRefunded          = "REFUNDED"
	ErrUnknown           = "UNKNOWN"
)

// Header is the fixed first row of every log file. Column order is stable and
// must not change so downstream tooling can rely on positional parsing.
var Header = []string{
	"seq",
	"tx_hash",
	"ts_utc",
	"batch_id",
	"mode",
	"chain",
	"wallet_from",
	"wallet_to",
	"token",
	"token_address",
	"amount_sent",
	"amount_received",
	"nonce",
	"gas_used",
	"fee_native",
	"status",
	"error",
	"explorer_url",
}

// Event is one transaction outcome emitted by a parallel wallet goroutine and
// serialized to a single CSV row by the writer goroutine. Monetary values are
// decimal strings in base units; no float is ever used.
type Event struct {
	TxHash         string
	TsUTC          time.Time
	BatchID        string
	Mode           string
	Chain          string
	WalletFrom     string
	WalletTo       string
	Token          string
	TokenAddress   string
	AmountSent     string
	AmountReceived string
	Nonce          string
	GasUsed        string
	FeeNative      string
	Status         Status
	Error          string
	ExplorerURL    string
}
