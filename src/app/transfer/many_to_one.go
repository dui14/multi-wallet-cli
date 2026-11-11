package transfer

import (
	"context"
	"fmt"
	"math/big"

	"multi-wallet-cli/domain/wallet"
)

func (s *Service) PlanManyToOne(ctx context.Context, req Request, signer KeySigner) (Plan, error) {
	flow := s.NewFlow(req.Chain)
	flow.signer = signer
	if err := flow.prepareEVM(req.From); err != nil {
		return Plan{}, err
	}
	return flow.buildManyToOne(ctx, req)
}

func (f *Flow) buildManyToOne(ctx context.Context, req Request) (Plan, error) {
	if err := f.requireRecipientEVM(req.To); err != nil {
		return Plan{}, err
	}
	recipient := normalizeEVMAddress(req.To)
	if len(req.Froms) == 0 {
		return Plan{}, fmt.Errorf("select at least one source wallet")
	}
	plan := f.emptyPlan(req)
	for _, item := range req.Tokens {
		phase := f.newPhase(item)
		for _, source := range req.Froms {
			line, skipReason, err := f.manyToOneLine(ctx, source, recipient, item)
			if err != nil {
				return Plan{}, err
			}
			if skipReason != "" {
				plan.Skipped = append(plan.Skipped, SkipRow{Wallet: source.Label, Token: item.Symbol, Reason: skipReason})
				continue
			}
			line.Nonce = uint64(len(plan.Lines))
			plan.Lines = append(plan.Lines, line)
			plan.Gas.Add(plan.Gas, line.Fee)
			phase.Sent++
		}
		plan.Phases = append(plan.Phases, phase)
	}
	if len(plan.Lines) == 0 {
		return Plan{}, fmt.Errorf("no wallets have transferable balance")
	}
	if err := f.finalizePlan(ctx, plan); err != nil {
		return Plan{}, err
	}
	return *plan, nil
}

func (f *Flow) manyToOneLine(ctx context.Context, source wallet.PublicWallet, recipient string, item TokenLine) (LinePlan, string, error) {
	resolved, err := f.loadMeta(ctx, item)
	if err != nil {
		return LinePlan{}, "", err
	}
	balance, err := f.balanceOf(ctx, source.Address, resolved)
	if err != nil {
		return LinePlan{}, "", err
	}
	if balance.Sign() == 0 {
		return LinePlan{}, "zero balance", nil
	}
	amount := new(big.Int).Set(balance)
	if resolved.Native {
		resolved.Max = true
		resolved.Keep = item.Keep
		amount = big.NewInt(0)
	} else if item.Keep != nil && item.Keep.Sign() > 0 && item.Keep.Cmp(balance) < 0 {
		amount = new(big.Int).Sub(balance, item.Keep)
	}
	if !resolved.Native && amount.Sign() <= 0 {
		return LinePlan{}, "zero balance", nil
	}
	line := f.transferLine(source, recipient, resolved, amount)
	line.Kind = KindTransfer
	line.Fee = big.NewInt(0)
	return line, "", nil
}
