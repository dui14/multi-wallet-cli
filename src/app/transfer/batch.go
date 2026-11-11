package transfer

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/evm"
)

type plannedChunk struct {
	path     BatchPath
	indexes  []int
	gasLimit uint64
	cost     *big.Int
	lineGas  []uint64
}

type indexGroup struct {
	owner   wallet.PublicWallet
	indexes []int
}

func selectBatchPath(calls int, chainReady bool, delegated bool, minCalls int, minDelegated int) BatchPath {
	if chainReady && delegated && calls >= minDelegated {
		return PathSelfCall
	}
	if chainReady && calls >= minCalls {
		return PathSetCode
	}
	return PathOrdinary
}

func assignNativeMax(balance *big.Int, gasCost *big.Int, keep *big.Int, assigned *big.Int, count int) ([]*big.Int, error) {
	if count <= 0 {
		return nil, nil
	}
	spendable := new(big.Int).Set(balance)
	for _, part := range []*big.Int{gasCost, keep, assigned} {
		if part != nil {
			spendable.Sub(spendable, part)
		}
	}
	if spendable.Sign() <= 0 {
		return nil, fmt.Errorf("balance too low to cover gas")
	}
	parts := distributeEven(spendable, count)
	for _, part := range parts {
		if part.Sign() <= 0 {
			return nil, fmt.Errorf("balance too low to cover gas")
		}
	}
	return parts, nil
}

func (f *Flow) finalizePlan(ctx context.Context, plan *Plan) error {
	if err := f.loadCapability(ctx); err != nil {
		return err
	}
	groups := groupIndexes(plan.Lines)
	kept := map[int]struct{}{}
	summaries := make([]OwnerSummary, 0, len(groups))
	totalGas := big.NewInt(0)
	for _, group := range groups {
		chunks, err := f.prepareOwner(ctx, plan, group)
		if err != nil {
			if plan.Request.Mode == ModeManyToOne {
				for _, index := range group.indexes {
					plan.Skipped = append(plan.Skipped, SkipRow{Wallet: group.owner.Label, Token: plan.Lines[index].Token.Symbol, Reason: err.Error()})
				}
				continue
			}
			return err
		}
		for ordinal, chunk := range chunks {
			f.writeChunk(plan, ordinal, chunk)
			if chunk.cost != nil {
				totalGas.Add(totalGas, chunk.cost)
			}
		}
		for _, index := range group.indexes {
			kept[index] = struct{}{}
		}
		summaries = append(summaries, summaryFor(group.owner.Label, chunks))
	}
	filtered := make([]LinePlan, 0, len(kept))
	for index, line := range plan.Lines {
		if _, ok := kept[index]; ok {
			filtered = append(filtered, line)
		}
	}
	if len(filtered) == 0 {
		return fmt.Errorf("no transfers to execute")
	}
	for index := range filtered {
		filtered[index].Nonce = uint64(index)
	}
	plan.Lines = filtered
	plan.Summaries = summaries
	plan.Gas = totalGas
	plan.Total = f.totalValue(plan)
	return nil
}

func (f *Flow) prepareOwner(ctx context.Context, plan *Plan, group indexGroup) ([]plannedChunk, error) {
	delegated, err := f.ownerDelegated(ctx, group.owner.Address)
	if err != nil {
		return nil, err
	}
	if err := f.applyNativeMax(ctx, plan, group, delegated); err != nil {
		return nil, err
	}
	chunks, err := f.fit(ctx, plan, group.indexes, delegated, false)
	if err != nil {
		return nil, err
	}
	if err := f.preflightOwner(ctx, plan, group, chunks); err != nil {
		return nil, err
	}
	return chunks, nil
}

func (f *Flow) applyNativeMax(ctx context.Context, plan *Plan, group indexGroup, delegated bool) error {
	maxIndexes := make([]int, 0)
	assigned := big.NewInt(0)
	keep := big.NewInt(0)
	for _, index := range group.indexes {
		line := plan.Lines[index]
		if line.Token.Native && line.Token.Max {
			maxIndexes = append(maxIndexes, index)
			if line.Token.Keep != nil && line.Token.Keep.Cmp(keep) > 0 {
				keep = new(big.Int).Set(line.Token.Keep)
			}
			continue
		}
		if line.Token.Native && line.Value != nil {
			assigned.Add(assigned, line.Value)
		}
	}
	if len(maxIndexes) == 0 {
		return nil
	}
	native := f.nativeItemOf(plan.Lines[maxIndexes[0]].Token)
	balance, err := f.balanceOf(ctx, group.owner.Address, native)
	if err != nil {
		return err
	}
	cost, err := f.ownerGasCost(ctx, plan, group.indexes, delegated, true)
	if err != nil {
		return err
	}
	parts, err := assignNativeMax(balance, cost, keep, assigned, len(maxIndexes))
	if err != nil {
		return fmt.Errorf("%s: %w", group.owner.Label, err)
	}
	for index, lineIndex := range maxIndexes {
		plan.Lines[lineIndex].Value = parts[index]
		plan.Lines[lineIndex].Token.Amount = new(big.Int).Set(parts[index])
	}
	revised, err := f.ownerGasCost(ctx, plan, group.indexes, delegated, false)
	if err != nil {
		return err
	}
	if revised.Cmp(cost) > 0 {
		parts, err = assignNativeMax(balance, revised, keep, assigned, len(maxIndexes))
		if err != nil {
			return fmt.Errorf("%s: %w", group.owner.Label, err)
		}
		for index, lineIndex := range maxIndexes {
			plan.Lines[lineIndex].Value = parts[index]
			plan.Lines[lineIndex].Token.Amount = new(big.Int).Set(parts[index])
		}
	}
	return nil
}

func (f *Flow) ownerGasCost(ctx context.Context, plan *Plan, indexes []int, delegated bool, zeroMax bool) (*big.Int, error) {
	chunks, err := f.fit(ctx, plan, indexes, delegated, zeroMax)
	if err != nil {
		return nil, err
	}
	total := big.NewInt(0)
	for _, chunk := range chunks {
		if chunk.cost != nil {
			total.Add(total, chunk.cost)
		}
	}
	return total, nil
}

func (f *Flow) fit(ctx context.Context, plan *Plan, indexes []int, delegated bool, zeroMax bool) ([]plannedChunk, error) {
	if len(indexes) == 0 {
		return nil, nil
	}
	lines := linesAt(plan, indexes)
	path := selectBatchPath(len(lines), f.chainReady, delegated, f.minCalls, f.minDelegated)
	gas, cost, perLine, err := f.estimateLines(ctx, lines, path, zeroMax)
	limit := f.blockGasLimit(ctx)
	if err != nil {
		if path != PathOrdinary && len(lines) > 1 && overGas(err) {
			return f.split(ctx, plan, indexes, delegated, zeroMax, limit)
		}
		return nil, f.explainFailure(ctx, lines, err)
	}
	if path != PathOrdinary && limit > 0 && gas > limit && len(lines) > 1 {
		return f.split(ctx, plan, indexes, delegated, zeroMax, limit)
	}
	if path == PathOrdinary && limit > 0 {
		for index, lineGas := range perLine {
			if lineGas > limit {
				return nil, fmt.Errorf("%s exceeds the block gas limit", lines[index].Token.Symbol)
			}
		}
	}
	return []plannedChunk{{path: path, indexes: append([]int(nil), indexes...), gasLimit: gas, cost: cost, lineGas: perLine}}, nil
}

func (f *Flow) split(ctx context.Context, plan *Plan, indexes []int, delegated bool, zeroMax bool, limit uint64) ([]plannedChunk, error) {
	best := 0
	low, high := 1, len(indexes)-1
	for low <= high {
		mid := (low + high) / 2
		lines := linesAt(plan, indexes[:mid])
		path := selectBatchPath(len(lines), f.chainReady, delegated, f.minCalls, f.minDelegated)
		gas, _, _, err := f.estimateLines(ctx, lines, path, zeroMax)
		if err == nil && (limit == 0 || gas <= limit) {
			best = mid
			low = mid + 1
			continue
		}
		high = mid - 1
	}
	if best == 0 {
		return nil, fmt.Errorf("%s exceeds the block gas limit", linesAt(plan, indexes[:1])[0].Token.Symbol)
	}
	left, err := f.fit(ctx, plan, indexes[:best], delegated, zeroMax)
	if err != nil {
		return nil, err
	}
	right, err := f.fit(ctx, plan, indexes[best:], delegated, zeroMax)
	if err != nil {
		return nil, err
	}
	return append(left, right...), nil
}

func (f *Flow) estimateLines(ctx context.Context, lines []LinePlan, path BatchPath, zeroMax bool) (uint64, *big.Int, []uint64, error) {
	price := f.feePrice(ctx)
	if price == nil || price.Sign() == 0 {
		return 0, nil, nil, fmt.Errorf("fee estimate unavailable")
	}
	if path == PathOrdinary {
		perLine := make([]uint64, len(lines))
		total := uint64(0)
		cost := big.NewInt(0)
		for index, line := range lines {
			tx, err := lineTransferTx(line, zeroMax)
			if err != nil {
				return 0, nil, nil, err
			}
			var estimate uint64
			if err := f.call(ctx, func(callCtx context.Context, rpcURL string) error {
				var gasErr error
				estimate, gasErr = f.evm.EstimateGasLimit(callCtx, rpcURL, tx)
				return gasErr
			}); err != nil {
				return 0, nil, nil, err
			}
			buffered := bufferGas(estimate)
			perLine[index] = buffered
			total += buffered
			cost.Add(cost, new(big.Int).Mul(new(big.Int).Set(price), new(big.Int).SetUint64(buffered)))
		}
		return total, cost, perLine, nil
	}
	tx, err := f.batchTransferTx(ctx, lines, path, zeroMax)
	if err != nil {
		return 0, nil, nil, err
	}
	var estimate uint64
	if err := f.call(ctx, func(callCtx context.Context, rpcURL string) error {
		var gasErr error
		estimate, gasErr = f.evm.EstimateGasLimit(callCtx, rpcURL, tx)
		return gasErr
	}); err != nil {
		return 0, nil, nil, err
	}
	buffered := bufferGas(estimate)
	return buffered, new(big.Int).Mul(new(big.Int).Set(price), new(big.Int).SetUint64(buffered)), nil, nil
}

func (f *Flow) explainFailure(ctx context.Context, lines []LinePlan, batchErr error) error {
	if len(lines) <= 1 {
		symbol := ""
		if len(lines) == 1 {
			symbol = lines[0].Token.Symbol
		}
		if symbol == "" {
			return batchErr
		}
		return fmt.Errorf("%s: %w", symbol, batchErr)
	}
	for index, line := range lines {
		tx, err := lineTransferTx(line, false)
		if err != nil {
			return fmt.Errorf("%s line %d: %w", line.Token.Symbol, index+1, err)
		}
		callErr := f.call(ctx, func(callCtx context.Context, rpcURL string) error {
			return f.evm.Simulate(callCtx, rpcURL, tx)
		})
		if callErr != nil {
			return fmt.Errorf("%s line %d: %w", line.Token.Symbol, index+1, callErr)
		}
	}
	return batchErr
}

func (f *Flow) preflightOwner(ctx context.Context, plan *Plan, group indexGroup, chunks []plannedChunk) error {
	gasCost := big.NewInt(0)
	for _, chunk := range chunks {
		if chunk.cost != nil {
			gasCost.Add(gasCost, chunk.cost)
		}
	}
	nativeValue := big.NewInt(0)
	tokens := map[string]*big.Int{}
	meta := map[string]TokenLine{}
	for _, index := range group.indexes {
		line := plan.Lines[index]
		if line.Token.Native {
			if line.Value != nil {
				nativeValue.Add(nativeValue, line.Value)
			}
			continue
		}
		key := strings.ToLower(line.Token.Address)
		if tokens[key] == nil {
			tokens[key] = big.NewInt(0)
			meta[key] = line.Token
		}
		if line.Value != nil {
			tokens[key].Add(tokens[key], line.Value)
		}
	}
	native := f.nativeItemOf(TokenLine{Native: true})
	balance, err := f.balanceOf(ctx, group.owner.Address, native)
	if err != nil {
		return err
	}
	need := new(big.Int).Add(nativeValue, gasCost)
	if balance.Cmp(need) < 0 {
		return fmt.Errorf("%s: native balance too low to cover value and gas", group.owner.Label)
	}
	for key, amount := range tokens {
		tokenBalance, balanceErr := f.balanceOf(ctx, group.owner.Address, meta[key])
		if balanceErr != nil {
			return balanceErr
		}
		if tokenBalance.Cmp(amount) < 0 {
			return fmt.Errorf("%s: %s balance too low", group.owner.Label, meta[key].Symbol)
		}
	}
	return nil
}

func (f *Flow) writeChunk(plan *Plan, ordinal int, chunk plannedChunk) {
	for offset, index := range chunk.indexes {
		plan.Lines[index].Path = chunk.path
		plan.Lines[index].Chunk = ordinal
		if chunk.path == PathOrdinary && offset < len(chunk.lineGas) {
			plan.Lines[index].GasLimit = chunk.lineGas[offset]
			total := uint64(0)
			for _, gas := range chunk.lineGas {
				total += gas
			}
			if chunk.cost != nil && total > 0 {
				plan.Lines[index].Fee = new(big.Int).Mul(chunk.cost, new(big.Int).SetUint64(chunk.lineGas[offset]))
				plan.Lines[index].Fee.Div(plan.Lines[index].Fee, new(big.Int).SetUint64(total))
			}
			continue
		}
		plan.Lines[index].GasLimit = chunk.gasLimit
		if offset == 0 && chunk.cost != nil {
			plan.Lines[index].Fee = new(big.Int).Set(chunk.cost)
		} else {
			plan.Lines[index].Fee = big.NewInt(0)
		}
	}
}

func (f *Flow) loadCapability(ctx context.Context) error {
	if f.capabilitySet {
		return f.capabilityErr
	}
	f.capabilitySet = true
	if f.executor == "" || f.executorHash == "" {
		return nil
	}
	var code []byte
	err := f.call(ctx, func(callCtx context.Context, rpcURL string) error {
		var codeErr error
		code, codeErr = f.evm.Code(callCtx, rpcURL, f.executor)
		return codeErr
	})
	if err != nil {
		return err
	}
	if matchErr := evm.ExecutorCodeMatches(code, f.executorHash); matchErr != nil {
		name := f.network.Name
		if name == "" {
			name = f.logChain
		}
		f.capabilityErr = fmt.Errorf("%s: %w", name, matchErr)
		return f.capabilityErr
	}
	var ready bool
	err = f.call(ctx, func(callCtx context.Context, rpcURL string) error {
		var supportErr error
		ready, supportErr = f.evm.SupportsSetCodeTx(callCtx, rpcURL)
		return supportErr
	})
	if err != nil {
		return err
	}
	f.chainReady = ready
	f.runtimeHash = crypto.Keccak256Hash(code).Hex()
	return nil
}

func (f *Flow) ownerDelegated(ctx context.Context, owner string) (bool, error) {
	if !f.chainReady {
		return false, nil
	}
	var code []byte
	err := f.call(ctx, func(callCtx context.Context, rpcURL string) error {
		var codeErr error
		code, codeErr = f.evm.Code(callCtx, rpcURL, owner)
		return codeErr
	})
	if err != nil {
		return false, err
	}
	return evm.DelegatedTo(code, common.HexToAddress(f.executor), common.HexToHash(f.runtimeHash)), nil
}

func (f *Flow) batchTransferTx(ctx context.Context, lines []LinePlan, path BatchPath, zeroMax bool) (evm.TransferTx, error) {
	if len(lines) == 0 {
		return evm.TransferTx{}, fmt.Errorf("batch is empty")
	}
	calls, err := callsFor(lines, zeroMax)
	if err != nil {
		return evm.TransferTx{}, err
	}
	data, err := evm.PackExecute(calls)
	if err != nil {
		return evm.TransferTx{}, err
	}
	owner := lines[0].Owner
	tx := evm.TransferTx{From: owner.Address, To: owner.Address, Value: big.NewInt(0), Data: data}
	if path != PathSetCode {
		return tx, nil
	}
	auth, err := f.signedAuthorization(ctx, owner)
	if err != nil {
		return evm.TransferTx{}, err
	}
	tx.Auth = &auth
	return tx, nil
}

func (f *Flow) signedAuthorization(ctx context.Context, owner wallet.PublicWallet) (types.SetCodeAuthorization, error) {
	if f.signer == nil {
		return types.SetCodeAuthorization{}, fmt.Errorf("signing key unavailable")
	}
	key, ok := f.signer(owner.Label)
	if !ok {
		return types.SetCodeAuthorization{}, fmt.Errorf("signing key unavailable")
	}
	defer zeroBytes(key)
	nonce, err := f.nonce(ctx, owner.Address)
	if err != nil {
		return types.SetCodeAuthorization{}, err
	}
	auth, err := evm.AuthorizationForOwner(f.chainID, f.executor, nonce)
	if err != nil {
		return types.SetCodeAuthorization{}, err
	}
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

func callsFor(lines []LinePlan, zeroMax bool) ([]evm.Call, error) {
	calls := make([]evm.Call, len(lines))
	for index, line := range lines {
		value := line.Value
		if value == nil {
			value = big.NewInt(0)
		}
		if line.Token.Native {
			if zeroMax && line.Token.Max {
				value = big.NewInt(0)
			}
			if !common.IsHexAddress(line.To) {
				return nil, fmt.Errorf("invalid recipient address")
			}
			calls[index] = evm.Call{To: common.HexToAddress(line.To), Value: new(big.Int).Set(value)}
			continue
		}
		amount := new(big.Int).Set(value)
		if amount.Sign() <= 0 {
			return nil, fmt.Errorf("%s amount must be positive", line.Token.Symbol)
		}
		data, err := evm.PackERC20Transfer(line.Recipient, amount)
		if err != nil {
			return nil, err
		}
		calls[index] = evm.Call{To: common.HexToAddress(line.Token.Address), Value: big.NewInt(0), Data: data}
	}
	return calls, nil
}

func lineTransferTx(line LinePlan, zeroMax bool) (evm.TransferTx, error) {
	if line.Token.Native {
		value := line.Value
		if value == nil || (zeroMax && line.Token.Max) {
			value = big.NewInt(0)
		}
		return evm.TransferTx{From: line.Owner.Address, To: line.To, Value: new(big.Int).Set(value), GasLimit: line.GasLimit}, nil
	}
	amount := line.Value
	if amount == nil {
		amount = big.NewInt(0)
	}
	data, err := evm.PackERC20Transfer(line.Recipient, amount)
	if err != nil {
		return evm.TransferTx{}, err
	}
	return evm.TransferTx{From: line.Owner.Address, To: line.Token.Address, Value: big.NewInt(0), Data: data, GasLimit: line.GasLimit}, nil
}

func groupIndexes(lines []LinePlan) []indexGroup {
	index := map[string]int{}
	groups := make([]indexGroup, 0)
	for position, line := range lines {
		slot, ok := index[line.Owner.Label]
		if !ok {
			slot = len(groups)
			index[line.Owner.Label] = slot
			groups = append(groups, indexGroup{owner: line.Owner})
		}
		groups[slot].indexes = append(groups[slot].indexes, position)
	}
	return groups
}

func linesAt(plan *Plan, indexes []int) []LinePlan {
	lines := make([]LinePlan, len(indexes))
	for index, position := range indexes {
		lines[index] = plan.Lines[position]
	}
	return lines
}

func summaryFor(label string, chunks []plannedChunk) OwnerSummary {
	summary := OwnerSummary{Wallet: label, Chunks: len(chunks), Gas: big.NewInt(0), Path: PathOrdinary.Label()}
	paths := map[string]struct{}{}
	for _, chunk := range chunks {
		summary.Calls += len(chunk.indexes)
		if chunk.cost != nil {
			summary.Gas.Add(summary.Gas, chunk.cost)
		}
		paths[chunk.path.Label()] = struct{}{}
		summary.Path = chunk.path.Label()
	}
	if len(paths) > 1 {
		summary.Path = "mixed"
	}
	return summary
}

func overGas(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "gas limit") || strings.Contains(text, "exceeds block") || strings.Contains(text, "out of gas")
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
