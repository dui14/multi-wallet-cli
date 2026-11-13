package rpc

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type txSeqKey struct{}

type TraceFunc func(TraceEvent)

type TraceEvent struct {
	Kind       string
	Tx         int
	Endpoint   string
	ElapsedMs  int64
	SetElapsed bool
	OK         bool
	SetOK      bool
	Error      string
	Source     string
	Note       string
}

type CooledEndpoint struct {
	URL       string
	Reason    string
	Remaining time.Duration
}

func WithTxSeq(ctx context.Context, seq int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, txSeqKey{}, seq)
}

func TxSeqFrom(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	seq, _ := ctx.Value(txSeqKey{}).(int)
	return seq
}

func (e TraceEvent) Fields() map[string]any {
	fields := map[string]any{"kind": e.Kind}
	if e.Tx > 0 {
		fields["tx"] = e.Tx
	}
	if e.Endpoint != "" {
		fields["endpoint"] = e.Endpoint
	}
	if e.SetElapsed {
		fields["elapsed_ms"] = e.ElapsedMs
	}
	if e.SetOK {
		fields["ok"] = e.OK
	}
	if e.Error != "" {
		fields["error"] = e.Error
	}
	if e.Source != "" {
		fields["source"] = e.Source
	}
	if e.Note != "" {
		fields["note"] = e.Note
	}
	return fields
}

func (p *Pool) SetTrace(fn TraceFunc) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.trace = fn
	p.mu.Unlock()
}

func (p *Pool) currentTrace() TraceFunc {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.trace
}

func (p *Pool) emit(event TraceEvent) {
	trace := p.currentTrace()
	if trace == nil {
		return
	}
	trace(event)
}

func (p *Pool) rememberCoolReason(rawURL, reason string) {
	if p == nil || strings.TrimSpace(reason) == "" {
		return
	}
	p.mu.Lock()
	if p.cooledReason == nil {
		p.cooledReason = map[string]string{}
	}
	p.cooledReason[rawURL] = reason
	p.mu.Unlock()
}

func (p *Pool) CooledEndpoints() []CooledEndpoint {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	out := make([]CooledEndpoint, 0)
	for rawURL, until := range p.cooledUntil {
		if !now.Before(until) {
			continue
		}
		out = append(out, CooledEndpoint{
			URL:       rawURL,
			Reason:    p.cooledReason[rawURL],
			Remaining: until.Sub(now),
		})
	}
	return out
}

func failureText(original string, err error) string {
	if strings.TrimSpace(original) != "" {
		return original
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

func sourceBeforeSuccess(tx int) string {
	if tx > 0 {
		return fmt.Sprintf("tx %d before Race got a success", tx)
	}
	return "before Race got a success"
}

func leftoverAfterSuccess(tx int) string {
	if tx > 0 {
		return fmt.Sprintf("tx %d after Race already succeeded (leftover request)", tx)
	}
	return "leftover request"
}

type raceOutcome struct {
	url      string
	err      error
	original string
	elapsed  time.Duration
}

func raceResultEvent(tx int, result raceOutcome) TraceEvent {
	event := TraceEvent{
		Kind:       "race_result",
		Tx:         tx,
		Endpoint:   result.url,
		ElapsedMs:  result.elapsed.Milliseconds(),
		SetElapsed: true,
		SetOK:      true,
		OK:         result.err == nil,
	}
	if result.err != nil {
		event.Error = failureText(result.original, result.err)
		return event
	}
	if result.original != "" {
		event.Note = result.original
	}
	return event
}

func (p *Pool) watchLeftovers(results <-chan raceOutcome, remaining int, tx int, source string) {
	if remaining <= 0 {
		return
	}
	trace := p.currentTrace()
	if trace == nil {
		return
	}
	go func() {
		for i := 0; i < remaining; i++ {
			result := <-results
			event := TraceEvent{
				Kind:       "race_leftover",
				Tx:         tx,
				Endpoint:   result.url,
				ElapsedMs:  result.elapsed.Milliseconds(),
				SetElapsed: true,
				SetOK:      true,
				OK:         result.err == nil,
				Source:     source,
			}
			if result.err != nil {
				event.Error = failureText(result.original, result.err)
			} else if result.original != "" {
				event.Note = result.original
			}
			trace(event)
		}
	}()
}
