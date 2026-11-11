package transfer

import (
	"context"
	"fmt"
	"math/big"
)

func (s *Service) PlanOneToOne(ctx context.Context, req Request, signer KeySigner) (Plan, error) {
	flow := s.NewFlow(req.Chain)
	flow.signer = signer
	if err := flow.prepareEVM(req.From); err != nil {
		return Plan{}, err
	}
	return flow.buildOneToOne(ctx, req)
}

func (f *Flow) buildOneToOne(ctx context.Context, req Request) (Plan, error) {
	if err := f.requireRecipientEVM(req.To); err != nil {
		return Plan{}, err
	}
	recipient := normalizeEVMAddress(req.To)
	plan := f.emptyPlan(req)
	appendLine := f.lineAppender(plan)
	tokenCount := len(req.Tokens)
	for index, item := range req.Tokens {
		if item.Native && item.Max && index != tokenCount-1 {
			return Plan{}, fmt.Errorf("native max must be the last token in the queue")
		}
		phase := f.newPhase(item)
		resolved, err := f.loadMeta(ctx, item)
		if err != nil {
			return Plan{}, err
		}
		var amount *big.Int
		if item.Native && item.Max {
			resolved.Max = true
			amount = big.NewInt(0)
		} else {
			balance, balanceErr := f.balanceOf(ctx, req.From.Address, resolved)
			if balanceErr != nil {
				return Plan{}, balanceErr
			}
			amount, err = f.resolveAmount(resolved, balance)
			if err != nil {
				return Plan{}, err
			}
		}
		resolved.Amount = amount
		transfer := f.transferLine(req.From, recipient, resolved, amount)
		transfer.Fee = big.NewInt(0)
		appendLine(transfer)
		phase.Token = resolved
		if resolved.Custom || resolved.Unverified {
			phase.FeeOnTransfer = true
		}
		plan.Phases = append(plan.Phases, phase)
	}
	if len(plan.Lines) == 0 {
		return Plan{}, fmt.Errorf("no transfers to execute")
	}
	if err := f.finalizePlan(ctx, plan); err != nil {
		return Plan{}, err
	}
	return *plan, nil
}
