package transfer

import (
	"context"
	"fmt"
	"math/big"
)

func (s *Service) PlanOneToMany(ctx context.Context, req Request, signer KeySigner) (Plan, error) {
	flow := s.NewFlow(req.Chain)
	flow.signer = signer
	if err := flow.prepareEVM(req.From); err != nil {
		return Plan{}, err
	}
	return flow.buildOneToMany(ctx, req)
}

func (f *Flow) buildOneToMany(ctx context.Context, req Request) (Plan, error) {
	recipients, err := f.requireRecipientsEVM(req.Recipients)
	if err != nil {
		return Plan{}, err
	}
	plan := f.emptyPlan(req)
	appendLine := f.lineAppender(plan)
	for _, item := range req.Tokens {
		if item.Native {
			continue
		}
		resolved, loadErr := f.loadMeta(ctx, item)
		if loadErr != nil {
			return Plan{}, loadErr
		}
		values, splitErr := splitAmounts(resolved, recipients, req.Split)
		if splitErr != nil {
			return Plan{}, splitErr
		}
		balance, balanceErr := f.balanceOf(ctx, req.From.Address, resolved)
		if balanceErr != nil {
			return Plan{}, balanceErr
		}
		need := big.NewInt(0)
		for _, value := range values {
			need.Add(need, value)
		}
		if need.Cmp(balance) > 0 {
			return Plan{}, fmt.Errorf("%s: need %s but balance is %s", resolved.Symbol, formatUnits(need, resolved.Decimals), formatUnits(balance, resolved.Decimals))
		}
		phase := f.newPhase(resolved)
		phase.Recipients = recipients
		phase.Values = values
		if resolved.Custom || resolved.Unverified {
			phase.FeeOnTransfer = true
		}
		for index, recipient := range recipients {
			transfer := f.transferLine(req.From, recipient, resolved, values[index])
			transfer.Fee = big.NewInt(0)
			appendLine(transfer)
		}
		plan.Phases = append(plan.Phases, phase)
	}
	if err := f.buildOneToManyNative(ctx, req, plan, appendLine, recipients); err != nil {
		return Plan{}, err
	}
	if len(plan.Lines) == 0 {
		return Plan{}, fmt.Errorf("no transfers to execute")
	}
	if err := f.finalizePlan(ctx, plan); err != nil {
		return Plan{}, err
	}
	return *plan, nil
}

func (f *Flow) buildOneToManyNative(ctx context.Context, req Request, plan *Plan, appendLine func(LinePlan), recipients []string) error {
	for _, item := range req.Tokens {
		if !item.Native {
			continue
		}
		nativeItem := f.nativeItemOf(item)
		balance, err := f.balanceOf(ctx, req.From.Address, nativeItem)
		if err != nil {
			return err
		}
		var values []*big.Int
		if item.Max {
			values = make([]*big.Int, len(recipients))
			for index := range values {
				values[index] = big.NewInt(0)
			}
		} else {
			values, err = splitAmounts(item, recipients, req.Split)
			if err != nil {
				return err
			}
			need := big.NewInt(0)
			for _, value := range values {
				need.Add(need, value)
			}
			if need.Cmp(balance) > 0 {
				return fmt.Errorf("%s: need %s but balance is %s", nativeItem.Symbol, formatUnits(need, nativeItem.Decimals), formatUnits(balance, nativeItem.Decimals))
			}
		}
		phase := f.newPhase(nativeItem)
		phase.Recipients = recipients
		phase.Values = values
		for index, recipient := range recipients {
			transfer := f.transferLine(req.From, recipient, nativeItem, values[index])
			if item.Max {
				transfer.Token.Max = true
			}
			transfer.Fee = big.NewInt(0)
			appendLine(transfer)
		}
		plan.Phases = append(plan.Phases, phase)
	}
	return nil
}
