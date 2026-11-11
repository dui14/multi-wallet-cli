package transfer

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/evm"
	"multi-wallet-cli/infra/rpc"
)

var receiptTimeout = 2 * time.Minute
var receiptPollInterval = 400 * time.Millisecond
var receiptCallTimeout = 5 * time.Second
var broadcastTimeout = 20 * time.Second
var mempoolWait = 12 * time.Second

const maxRetry = 3

// KeySigner resolves a wallet label to its signing key at execution time.
type KeySigner func(label string) ([]byte, bool)

// Execute signs and broadcasts the plan, tracks receipts, and reports per line.
func (s *Service) Execute(ctx context.Context, plan Plan, signer KeySigner) (Report, error) {
	flow := s.NewFlow(plan.Request.Chain)
	if flow.RPC() == "" {
		return Report{}, fmt.Errorf("chain not found")
	}
	return flow.Execute(ctx, plan, signer)
}

func (f *Flow) Execute(ctx context.Context, plan Plan, signer KeySigner) (Report, error) {
	f.signer = signer
	lines := append([]LinePlan(nil), plan.Lines...)
	f.logMode = plan.Request.Mode
	f.logBatch = newBatchID()
	return f.broadcast(ctx, lines, signer, map[string]uint64{})
}

func (f *Flow) broadcast(ctx context.Context, lines []LinePlan, signer KeySigner, nonces map[string]uint64) (Report, error) {
	if f.logBatch == "" {
		f.logBatch = newBatchID()
	}
	groups := groupByOwner(lines)
	report := Report{Results: make([]OwnerResult, 0, len(groups)), Failed: []LinePlan{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	limiter := make(chan struct{}, 6)
	for _, group := range groups {
		wg.Add(1)
		go func(group ownerGroup) {
			defer wg.Done()
			limiter <- struct{}{}
			defer func() { <-limiter }()
			result := f.runOwner(ctx, group, signer, nonces)
			mu.Lock()
			report.Results = append(report.Results, result)
			for _, line := range group.lines {
				if line.pending {
					report.Failed = append(report.Failed, line.plan)
				}
			}
			mu.Unlock()
		}(group)
	}
	wg.Wait()
	sort.Slice(report.Results, func(i, j int) bool {
		return report.Results[i].Wallet < report.Results[j].Wallet
	})
	return report, nil
}

type ownerGroup struct {
	owner wallet.PublicWallet
	lines []pendingLine
}

type pendingLine struct {
	plan    LinePlan
	pending bool
	err     error
}

func groupByOwner(lines []LinePlan) []ownerGroup {
	index := map[string]int{}
	groups := make([]ownerGroup, 0)
	for _, line := range lines {
		position, ok := index[line.Owner.Label]
		if !ok {
			position = len(groups)
			index[line.Owner.Label] = position
			groups = append(groups, ownerGroup{owner: line.Owner})
		}
		groups[position].lines = append(groups[position].lines, pendingLine{plan: line, pending: true})
	}
	return groups
}

type receiptJob struct {
	hash    string
	nonce   uint64
	tx      *types.Transaction
	indexes []int
}

func (f *Flow) runOwner(ctx context.Context, group ownerGroup, signer KeySigner, _ map[string]uint64) OwnerResult {
	result := OwnerResult{Wallet: group.owner.Label, Lines: make([]LineResult, len(group.lines))}
	failAll := func(err error) OwnerResult {
		for index, line := range group.lines {
			outcome := lineOutcome{err: err, nonce: line.plan.Nonce}
			result.Lines[index] = lineResult(line.plan, outcome)
			group.lines[index].pending = true
			group.lines[index].err = err
			f.emitLine(group.owner, line.plan, outcome)
		}
		return result
	}
	key, ok := signer(group.owner.Label)
	if !ok {
		return failAll(fmt.Errorf("signing key unavailable"))
	}
	defer zeroBytes(key)
	if err := f.loadCapability(ctx); err != nil {
		return failAll(err)
	}
	var nonce uint64
	nonceCtx, cancel := context.WithTimeout(ctx, receiptCallTimeout)
	err := f.call(nonceCtx, func(callCtx context.Context, rpcURL string) error {
		var nonceErr error
		nonce, nonceErr = f.evm.PendingNonce(callCtx, rpcURL, group.owner.Address)
		return nonceErr
	})
	cancel()
	if err != nil {
		return failAll(err)
	}
	var jobs []receiptJob
	stopped := false
	var stopErr error
	for _, indexes := range chunkIndexes(group.lines) {
		if stopped {
			for _, index := range indexes {
				outcome := lineOutcome{err: stopErr, nonce: nonce}
				result.Lines[index] = lineResult(group.lines[index].plan, outcome)
				group.lines[index].pending = true
				group.lines[index].err = stopErr
				f.emitLine(group.owner, group.lines[index].plan, outcome)
			}
			continue
		}
		path := group.lines[indexes[0]].plan.Path
		if path == 0 {
			path = PathOrdinary
		}
		if path == PathOrdinary {
			signed, signErr := f.signOrdinary(ctx, group, key, indexes, nonce)
			if signErr != nil {
				stopped = true
				stopErr = fmt.Errorf("stopped after a previous send error")
				for _, index := range indexes {
					outcome := lineOutcome{err: signErr, nonce: nonce}
					result.Lines[index] = lineResult(group.lines[index].plan, outcome)
					group.lines[index].pending = true
					group.lines[index].err = signErr
					f.emitLine(group.owner, group.lines[index].plan, outcome)
				}
				continue
			}
			for offset, tx := range signed {
				index := indexes[offset]
				var hash string
				var hashMu sync.Mutex
				sendErr := f.blastTx(ctx, tx, &hash, &hashMu)
				if hash == "" && tx != nil {
					hash = tx.Hash().Hex()
				}
				if sendErr != nil {
					stopped = true
					stopErr = fmt.Errorf("stopped after a previous send error")
					outcome := lineOutcome{err: sendErr, nonce: nonce + uint64(offset)}
					result.Lines[index] = lineResult(group.lines[index].plan, outcome)
					group.lines[index].pending = true
					group.lines[index].err = sendErr
					f.emitLine(group.owner, group.lines[index].plan, outcome)
					for restOffset, rest := range indexes[offset+1:] {
						restOutcome := lineOutcome{err: stopErr, nonce: nonce + uint64(offset+1+restOffset)}
						result.Lines[rest] = lineResult(group.lines[rest].plan, restOutcome)
						group.lines[rest].pending = true
						group.lines[rest].err = stopErr
						f.emitLine(group.owner, group.lines[rest].plan, restOutcome)
					}
					break
				}
				jobs = append(jobs, receiptJob{hash: hash, nonce: nonce + uint64(offset), tx: tx, indexes: []int{index}})
			}
			if !stopped {
				nonce += uint64(len(signed))
			}
			continue
		}
		tx, sendErr := f.signBatch(ctx, group, key, indexes, nonce, path)
		if sendErr != nil {
			stopped = true
			stopErr = fmt.Errorf("stopped after a previous send error")
			for _, index := range indexes {
				outcome := lineOutcome{err: sendErr, nonce: nonce}
				result.Lines[index] = lineResult(group.lines[index].plan, outcome)
				group.lines[index].pending = true
				group.lines[index].err = sendErr
				f.emitLine(group.owner, group.lines[index].plan, outcome)
			}
			continue
		}
		var hash string
		var hashMu sync.Mutex
		sendErr = f.blastTx(ctx, tx, &hash, &hashMu)
		if hash == "" {
			hash = tx.Hash().Hex()
		}
		if sendErr != nil {
			stopped = true
			stopErr = fmt.Errorf("stopped after a previous send error")
			for _, index := range indexes {
				outcome := lineOutcome{txHash: hash, err: sendErr, nonce: nonce}
				result.Lines[index] = lineResult(group.lines[index].plan, outcome)
				group.lines[index].pending = true
				group.lines[index].err = sendErr
				f.emitLine(group.owner, group.lines[index].plan, outcome)
			}
			continue
		}
		jobs = append(jobs, receiptJob{hash: hash, nonce: nonce, tx: tx, indexes: append([]int(nil), indexes...)})
		if path == PathSetCode {
			nonce += 2
		} else {
			nonce++
		}
	}
	for _, job := range jobs {
		if !f.waitSeen(ctx, job.hash, mempoolWait) {
			var ignored string
			var hashMu sync.Mutex
			_ = f.blastTx(ctx, job.tx, &ignored, &hashMu)
		}
		receipt, receiptErr := f.waitReceipt(ctx, job.hash)
		outcome := outcomeFromReceipt(job.hash, job.nonce, receipt, receiptErr)
		for _, index := range job.indexes {
			result.Lines[index] = lineResult(group.lines[index].plan, outcome)
			group.lines[index].pending = outcome.err != nil
			group.lines[index].err = outcome.err
			group.lines[index].plan.Nonce = outcome.nonce
			f.emitLine(group.owner, group.lines[index].plan, outcome)
		}
	}
	return result
}

func (f *Flow) signOrdinary(ctx context.Context, group ownerGroup, key []byte, indexes []int, nonce uint64) ([]*types.Transaction, error) {
	signed := make([]*types.Transaction, len(indexes))
	for offset, index := range indexes {
		line := group.lines[index].plan
		txData, err := lineTransferTx(line, false)
		if err != nil {
			return nil, err
		}
		if line.GasLimit > 0 {
			txData.GasLimit = line.GasLimit
		} else {
			var limit uint64
			if err := f.call(ctx, func(callCtx context.Context, rpcURL string) error {
				var limitErr error
				limit, limitErr = f.evm.EstimateGasLimit(callCtx, rpcURL, txData)
				return limitErr
			}); err != nil {
				return nil, err
			}
			txData.GasLimit = bufferGas(limit)
		}
		fees, err := f.estimateFeesForSend(ctx)
		if err != nil {
			return nil, err
		}
		if line.Token.Native && line.Token.Max {
			clamped, clampErr := f.clampNativeMax(ctx, group.owner.Address, txData.Value, txData.GasLimit, fees)
			if clampErr != nil {
				return nil, clampErr
			}
			txData.Value = clamped
		}
		signed[offset], err = evm.SignTx(key, f.chainID, nonce+uint64(offset), txData, fees, f.EIP1559())
		if err != nil {
			return nil, err
		}
	}
	return signed, nil
}

func (f *Flow) signBatch(ctx context.Context, group ownerGroup, key []byte, indexes []int, nonce uint64, path BatchPath) (*types.Transaction, error) {
	lines := make([]LinePlan, len(indexes))
	for offset, index := range indexes {
		lines[offset] = group.lines[index].plan
	}
	calls, err := callsFor(lines, false)
	if err != nil {
		return nil, err
	}
	data, err := evm.PackExecute(calls)
	if err != nil {
		return nil, err
	}
	txData := evm.TransferTx{From: group.owner.Address, To: group.owner.Address, Value: big.NewInt(0), Data: data, GasLimit: lines[0].GasLimit}
	if txData.GasLimit == 0 {
		if path == PathSetCode {
			auth, authErr := evm.AuthorizationForOwner(f.chainID, f.executor, nonce)
			if authErr != nil {
				return nil, authErr
			}
			signedAuth, signErr := f.signAuth(key, auth)
			if signErr != nil {
				return nil, signErr
			}
			txData.Auth = &signedAuth
		}
		var limit uint64
		if err := f.call(ctx, func(callCtx context.Context, rpcURL string) error {
			var limitErr error
			limit, limitErr = f.evm.EstimateGasLimit(callCtx, rpcURL, txData)
			return limitErr
		}); err != nil {
			return nil, f.explainFailure(ctx, lines, err)
		}
		txData.GasLimit = bufferGas(limit)
		txData.Auth = nil
	}
	fees, err := f.estimateFeesForSend(ctx)
	if err != nil {
		return nil, err
	}
	if path == PathSetCode {
		auth, authErr := evm.AuthorizationForOwner(f.chainID, f.executor, nonce)
		if authErr != nil {
			return nil, authErr
		}
		return evm.SignSetCodeTx(key, f.chainID, nonce, txData, fees, auth)
	}
	return evm.SignTx(key, f.chainID, nonce, txData, fees, true)
}

func (f *Flow) signAuth(key []byte, auth types.SetCodeAuthorization) (types.SetCodeAuthorization, error) {
	privateKey, err := crypto.ToECDSA(key)
	if err != nil {
		return types.SetCodeAuthorization{}, fmt.Errorf("invalid private key")
	}
	signed, err := types.SignSetCode(privateKey, auth)
	if err != nil {
		return types.SetCodeAuthorization{}, fmt.Errorf("sign authorization: %w", err)
	}
	return signed, nil
}

func chunkIndexes(lines []pendingLine) [][]int {
	if len(lines) == 0 {
		return nil
	}
	groups := make([][]int, 0)
	start := 0
	for index := 1; index <= len(lines); index++ {
		if index == len(lines) || lines[index].plan.Chunk != lines[start].plan.Chunk || lines[index].plan.Path != lines[start].plan.Path {
			chunk := make([]int, 0, index-start)
			for position := start; position < index; position++ {
				chunk = append(chunk, position)
			}
			groups = append(groups, chunk)
			start = index
		}
	}
	return groups
}

func lineResult(line LinePlan, outcome lineOutcome) LineResult {
	return LineResult{
		Kind:      line.Kind,
		Token:     line.Token.Symbol,
		Amount:    line.Value,
		Decimals:  line.Token.Decimals,
		Recipient: line.Recipient,
		TxHash:    outcome.txHash,
		Status:    outcome.status,
		Block:     outcome.block,
		Err:       outcome.err,
	}
}

func outcomeFromReceipt(hash string, nonce uint64, receipt *evm.Receipt, err error) lineOutcome {
	if err != nil {
		return lineOutcome{txHash: hash, err: err, nonce: nonce}
	}
	if receipt == nil {
		return lineOutcome{txHash: hash, err: fmt.Errorf("receipt timeout"), nonce: nonce}
	}
	if receipt.Status != 1 {
		return lineOutcome{txHash: hash, status: int(receipt.Status), block: receipt.BlockNumber, gasUsed: receipt.GasUsed, effectiveGasPrice: receipt.EffectiveGasPrice, err: fmt.Errorf("reverted on-chain"), nonce: nonce}
	}
	return lineOutcome{txHash: hash, status: int(receipt.Status), block: receipt.BlockNumber, gasUsed: receipt.GasUsed, effectiveGasPrice: receipt.EffectiveGasPrice, nonce: nonce}
}

type lineOutcome struct {
	txHash            string
	status            int
	block             *big.Int
	nonce             uint64
	gasUsed           *big.Int
	effectiveGasPrice *big.Int
	err               error
}

func (f *Flow) blastTx(ctx context.Context, tx *types.Transaction, hash *string, hashMu *sync.Mutex) error {
	broadcastCtx, cancel := context.WithTimeout(ctx, broadcastTimeout)
	go func() {
		<-broadcastCtx.Done()
		cancel()
	}()
	return f.race(broadcastCtx, func(callCtx context.Context, rpcURL string) error {
		got, broadcastErr := f.evm.BroadcastTx(callCtx, rpcURL, tx)
		if broadcastErr != nil {
			return broadcastErr
		}
		hashMu.Lock()
		if *hash == "" {
			*hash = got
		}
		hashMu.Unlock()
		return nil
	})
}

func (f *Flow) waitSeen(ctx context.Context, txHash string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if f.txSeen(ctx, txHash) {
			return true
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return false
		}
		timer := time.NewTimer(receiptPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}

func (f *Flow) txSeen(ctx context.Context, txHash string) bool {
	pollCtx, cancel := context.WithTimeout(ctx, receiptCallTimeout)
	defer cancel()
	err := f.race(pollCtx, func(callCtx context.Context, rpcURL string) error {
		found, getErr := f.evm.HasTransaction(callCtx, rpcURL, txHash)
		if getErr != nil {
			return getErr
		}
		if !found {
			return rpc.ErrNotReady
		}
		return nil
	})
	return err == nil
}

func (f *Flow) waitReceipt(ctx context.Context, txHash string) (*evm.Receipt, error) {
	deadline := time.Now().Add(receiptTimeout)
	for {
		var receipt *evm.Receipt
		var mu sync.Mutex
		err := f.race(ctx, func(callCtx context.Context, rpcURL string) error {
			pollCtx, cancel := context.WithTimeout(callCtx, receiptCallTimeout)
			defer cancel()
			got, getErr := f.evm.ConfirmTx(pollCtx, rpcURL, txHash)
			if getErr != nil {
				return getErr
			}
			if got == nil {
				return rpc.ErrNotReady
			}
			mu.Lock()
			if receipt == nil {
				receipt = got
			}
			mu.Unlock()
			return nil
		})
		if err == nil && receipt != nil {
			return receipt, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			if err != nil && !errors.Is(err, rpc.ErrNotReady) {
				return nil, err
			}
			return nil, nil
		}
		timer := time.NewTimer(receiptPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (f *Flow) clampNativeMax(ctx context.Context, owner string, value *big.Int, gasLimit uint64, fees *evm.FeeEstimate) (*big.Int, error) {
	var balance *big.Int
	if err := f.call(ctx, func(callCtx context.Context, rpcURL string) error {
		pollCtx, cancel := context.WithTimeout(callCtx, receiptCallTimeout)
		defer cancel()
		var balanceErr error
		balance, balanceErr = f.evm.NativeBalance(pollCtx, rpcURL, owner)
		return balanceErr
	}); err != nil {
		return nil, err
	}
	reserve := new(big.Int).Mul(feeCap(fees, f.EIP1559()), new(big.Int).SetUint64(gasLimit))
	left := new(big.Int).Sub(balance, reserve)
	if left.Sign() <= 0 {
		return nil, fmt.Errorf("balance too low to cover gas")
	}
	if value != nil && value.Cmp(left) <= 0 && value.Sign() > 0 {
		return value, nil
	}
	return left, nil
}

// Retry re-broadcasts only the failed lines of a report.
func (s *Service) Retry(ctx context.Context, plan Plan, failed []LinePlan, signer KeySigner) (Report, error) {
	flow := s.NewFlow(plan.Request.Chain)
	if flow.RPC() == "" {
		return Report{}, fmt.Errorf("chain not found")
	}
	flow.logMode = plan.Request.Mode
	return flow.broadcast(ctx, failed, signer, map[string]uint64{})
}
