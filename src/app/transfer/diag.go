package transfer

import (
	"sort"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/core/types"

	"multi-wallet-cli/infra/logcsv"
	"multi-wallet-cli/infra/rpc"
)

func (s *Service) bindPoolTraceLocked(logger *logcsv.Writer) {
	for _, pool := range s.pools {
		bindPoolTrace(pool, logger)
	}
}

func bindPoolTrace(pool *rpc.Pool, logger *logcsv.Writer) {
	if pool == nil {
		return
	}
	if logger == nil {
		pool.SetTrace(nil)
		return
	}
	pool.SetTrace(func(event rpc.TraceEvent) {
		logger.WriteDiag(event.Fields())
	})
}

func (f *Flow) logBlastStart(seq int, tx *types.Transaction) {
	if f == nil || f.logger == nil {
		return
	}
	fields := map[string]any{
		"kind": "blast_start",
		"tx":   seq,
	}
	if tx != nil {
		fields["nonce"] = strconv.FormatUint(tx.Nonce(), 10)
	}
	if f.pool != nil {
		healthy := f.pool.LiveURLs()
		if healthy == nil {
			healthy = []string{}
		}
		fields["healthy"] = healthy
		cooled := f.pool.CooledEndpoints()
		rows := make([]map[string]any, 0, len(cooled))
		sort.Slice(cooled, func(i, j int) bool { return cooled[i].URL < cooled[j].URL })
		for _, item := range cooled {
			rows = append(rows, map[string]any{
				"endpoint":     item.URL,
				"reason":       item.Reason,
				"remaining_ms": item.Remaining.Milliseconds(),
			})
		}
		fields["cooled"] = rows
	}
	f.logger.WriteDiag(fields)
}

func (f *Flow) logBlastDone(seq int, elapsed time.Duration, err error) {
	if f == nil || f.logger == nil {
		return
	}
	fields := map[string]any{
		"kind":       "blast_done",
		"tx":         seq,
		"elapsed_ms": elapsed.Milliseconds(),
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	f.logger.WriteDiag(fields)
}
