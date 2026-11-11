package relayexec

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/aggregator/relay"
	"multi-wallet-cli/infra/config"
	"multi-wallet-cli/infra/evm"
	"multi-wallet-cli/infra/logcsv"
)

const statusPollInterval = time.Second
const statusPollTimeout = 5 * time.Minute

// KeySigner resolves a wallet label to its signing key at execution time.
type KeySigner func(label string) ([]byte, bool)

// Engine signs Relay transaction steps, broadcasts them through the matching
// chain's RPC, and polls the intent to a terminal status. Both swap and bridge
// share it so their execution and logging behave identically.
type Engine struct {
	relay   *relay.Client
	evm     *evm.Client
	signer  KeySigner
	chainBy func(relayChainID int64) (config.Network, bool)
}

// NewEngine builds the shared Relay execution engine.
func NewEngine(client *relay.Client, evmClient *evm.Client, signer KeySigner, chainBy func(relayChainID int64) (config.Network, bool)) *Engine {
	return &Engine{relay: client, evm: evmClient, signer: signer, chainBy: chainBy}
}

// Relay exposes the underlying Relay client so services can fetch quotes.
func (e *Engine) Relay() *relay.Client { return e.relay }

// Execute signs each Relay transaction step in order for one wallet, broadcasts
// them, then polls the intent to a terminal status. It returns the first source
// transaction hash, the mapped log status, and a normalized error code.
func (e *Engine) Execute(ctx context.Context, originNetwork config.Network, key []byte, owner wallet.PublicWallet, response *relay.QuoteResponse) (string, logcsv.Status, string) {
	nonce, err := e.evm.PendingNonce(ctx, originNetwork.PrimaryRPC(), owner.Address)
	if err != nil {
		return "", logcsv.StatusSkipped, logcsv.ErrRPCTimeout
	}
	sourceHash := ""
	for _, step := range response.Steps {
		if step.Kind != "transaction" {
			continue
		}
		for _, item := range step.Items {
			if item.Status == "complete" {
				continue
			}
			hash, sendErr := e.signAndSend(ctx, originNetwork, key, owner, item.Data, nonce)
			if sendErr != nil {
				return sourceHash, logcsv.StatusSkipped, NormalizeError(sendErr)
			}
			if sourceHash == "" {
				sourceHash = hash
			}
			nonce++
		}
	}
	if sourceHash == "" {
		return "", logcsv.StatusSkipped, logcsv.ErrUnknown
	}
	status, code := e.PollStatus(ctx, response.RequestID)
	return sourceHash, status, code
}

func (e *Engine) signAndSend(ctx context.Context, originNetwork config.Network, key []byte, owner wallet.PublicWallet, data relay.StepItemData, nonce uint64) (string, error) {
	stepNetwork := originNetwork
	if resolved, ok := e.chainBy(data.ChainID); ok {
		stepNetwork = resolved
	}
	if stepNetwork.VMType == "svm" {
		return "", fmt.Errorf("solana relay steps are not supported by this build")
	}
	value := ParseQuantity(data.Value)
	gasLimit := ParseQuantityUint(data.Gas)
	fees := &evm.FeeEstimate{}
	if price := ParseQuantity(data.MaxFeePerGas); price != nil {
		fees.GasPrice = price
		fees.MaxFeePerGas = price
		fees.MaxPriorityFeePerGas = big.NewInt(0)
	}
	signed, err := evm.SignTx(key, stepNetwork.ChainID, nonce, evm.TransferTx{
		From:     owner.Address,
		To:       data.To,
		Value:    value,
		Data:     DecodeHex(data.Data),
		GasLimit: gasLimit,
	}, fees, false)
	if err != nil {
		return "", err
	}
	return e.evm.BroadcastTx(ctx, stepNetwork.PrimaryRPC(), signed)
}

// PollStatus waits for a Relay intent to reach success, failure, or refund,
// mapping the terminal state onto a log status. A same-chain swap resolves
// almost immediately; a bridge may take longer.
func (e *Engine) PollStatus(ctx context.Context, requestID string) (logcsv.Status, string) {
	if requestID == "" {
		return logcsv.StatusSubmitted, ""
	}
	deadline := time.Now().Add(statusPollTimeout)
	for {
		status, err := e.relay.Status(ctx, requestID)
		if err == nil && status != nil {
			switch status.Status {
			case relay.StatusSuccess:
				return logcsv.StatusBridgeCompleted, ""
			case relay.StatusFailure:
				return logcsv.StatusSubmitted, logcsv.ErrUnknown
			case relay.StatusRefund:
				return logcsv.StatusBridgeRefunded, logcsv.ErrRefunded
			}
		}
		if time.Now().After(deadline) {
			return logcsv.StatusBridgePending, ""
		}
		select {
		case <-ctx.Done():
			return logcsv.StatusBridgePending, ""
		case <-time.After(statusPollInterval):
		}
	}
}

// Signer exposes the engine's key signer.
func (e *Engine) Signer(label string) ([]byte, bool) {
	if e.signer == nil {
		return nil, false
	}
	return e.signer(label)
}

// Log emits one swap/bridge event with a fresh batch id.
func (e *Engine) Log(logger *logcsv.Writer, event logcsv.Event) {
	if logger == nil {
		return
	}
	event.BatchID = NewBatchID()
	logger.Emit(event)
}

// NormalizeError maps a Relay or transport error onto a stable log error code.
func NormalizeError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "rate limited"):
		return logcsv.ErrRPCTimeout
	case strings.Contains(message, "no route"), strings.Contains(message, "no steps"):
		return logcsv.ErrNoRoute
	case strings.Contains(message, "insufficient"):
		return logcsv.ErrInsufficientFunds
	case strings.Contains(message, "stale"), strings.Contains(message, "expired"):
		return logcsv.ErrQuoteExpired
	case strings.Contains(message, "timeout"), strings.Contains(message, "deadline"):
		return logcsv.ErrRPCTimeout
	default:
		return logcsv.ErrUnknown
	}
}
