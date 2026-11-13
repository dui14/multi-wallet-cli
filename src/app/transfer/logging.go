package transfer

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/logcsv"
)

func newBatchID() string {
	buffer := make([]byte, 6)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("batch-%d", time.Now().UTC().UnixNano())
	}
	return "batch-" + time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(buffer)
}

// AttachLogger wires a CSV transaction logger into the service so executed
// transfers are persisted. Passing nil disables logging.
func (s *Service) AttachLogger(logger *logcsv.Writer) {
	s.mu.Lock()
	s.logger = logger
	s.bindPoolTraceLocked(logger)
	s.mu.Unlock()
}

func (s *Service) activeLogger() *logcsv.Writer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.logger
}

func (f *Flow) emitLine(owner wallet.PublicWallet, line LinePlan, outcome lineOutcome) {
	logger := f.logger
	if logger == nil {
		return
	}
	status, code := classifyOutcome(outcome)
	sent := formatBaseUnits(line.Value)
	received := "0"
	if status == logcsv.StatusSuccess {
		received = sent
	}
	to := line.Recipient
	if to == "" {
		to = line.To
	}
	explorer := ""
	if outcome.txHash != "" && f.network.Explorer != "" {
		explorer = strings.TrimRight(f.network.Explorer, "/") + "/tx/" + outcome.txHash
	}
	logger.Emit(logcsv.Event{
		TxHash:         outcome.txHash,
		TsUTC:          time.Now().UTC(),
		BatchID:        f.logBatch,
		Mode:           f.logMode,
		Chain:          f.logChain,
		WalletFrom:     owner.Label,
		WalletTo:       to,
		Token:          line.Token.Symbol,
		TokenAddress:   line.Token.Address,
		AmountSent:     sent,
		AmountReceived: received,
		Nonce:          formatUint(outcome.nonce),
		GasUsed:        formatBaseUnits(outcome.gasUsed),
		FeeNative:      feeNative(outcome),
		Status:         status,
		Error:          code,
		ExplorerURL:    explorer,
	})
}

func classifyOutcome(outcome lineOutcome) (logcsv.Status, string) {
	if outcome.err == nil && outcome.status == 1 {
		return logcsv.StatusSuccess, ""
	}
	if outcome.txHash == "" {
		return logcsv.StatusSkipped, normalizeTransferError(outcome.err)
	}
	if outcome.status == 0 && outcome.block != nil {
		return logcsv.StatusReverted, logcsv.ErrReverted
	}
	return logcsv.StatusSubmitted, normalizeTransferError(outcome.err)
}

func normalizeTransferError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "insufficient funds"):
		return logcsv.ErrInsufficientFunds
	case strings.Contains(message, "revert"):
		return logcsv.ErrReverted
	case strings.Contains(message, "receipt timeout"), strings.Contains(message, "timeout"), strings.Contains(message, "deadline"):
		return logcsv.ErrRPCTimeout
	case strings.Contains(message, "nonce too low"), strings.Contains(message, "replacement"):
		return logcsv.ErrReplaced
	case strings.Contains(message, "signing key"):
		return logcsv.ErrSigningFailed
	case strings.Contains(message, "broadcast"):
		return logcsv.ErrBroadcastFailed
	default:
		return logcsv.ErrUnknown
	}
}

func feeNative(outcome lineOutcome) string {
	if outcome.gasUsed == nil || outcome.effectiveGasPrice == nil {
		return "0"
	}
	fee := new(big.Int).Mul(outcome.gasUsed, outcome.effectiveGasPrice)
	return fee.String()
}

func formatBaseUnits(value *big.Int) string {
	if value == nil {
		return "0"
	}
	return value.String()
}

func formatUint(value uint64) string {
	if value == 0 {
		return "0"
	}
	return new(big.Int).SetUint64(value).String()
}
