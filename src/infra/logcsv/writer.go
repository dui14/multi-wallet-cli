package logcsv

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"multi-wallet-cli/infra/logging"
)

const channelBuffer = 512

var maxRowsPerFile = 10000

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

type state struct {
	Seq  int `json:"seq"`
	File int `json:"file"`
	Rows int `json:"rows"`
}

// Writer persists transaction events to sequentially numbered CSV files using a
// single background goroutine. Parallel wallet goroutines call Emit; only the
// writer goroutine touches the file so rows never interleave.
type Writer struct {
	dir       string
	statePath string

	events chan Event
	quit   chan struct{}
	done   chan struct{}
	once   sync.Once

	seq  int
	file int
	rows int

	handle     *os.File
	csvw       *csv.Writer
	err        error
	diag       chan map[string]any
	kick       chan struct{}
	spill      []map[string]any
	spillMu    sync.Mutex
	diagHandle *os.File
}

// NewWriter prepares the logs directory, restores the persisted position from
// state.json, and starts the single writer goroutine.
func NewWriter(dir string) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create logs dir: %w", err)
	}
	writer := &Writer{
		dir:       dir,
		statePath: filepath.Join(dir, "state.json"),
		events:    make(chan Event, channelBuffer),
		quit:      make(chan struct{}),
		done:      make(chan struct{}),
		diag:      make(chan map[string]any, diagBuffer),
		kick:      make(chan struct{}, 1),
	}
	writer.loadState()
	go writer.run()
	return writer, nil
}

func (w *Writer) loadState() {
	data, err := os.ReadFile(w.statePath)
	if err != nil {
		w.seq = 0
		w.file = 1
		w.rows = 0
		return
	}
	var loaded state
	if err := json.Unmarshal(data, &loaded); err != nil {
		w.seq = 0
		w.file = 1
		w.rows = 0
		return
	}
	if loaded.File < 1 {
		loaded.File = 1
	}
	if loaded.Rows < 0 {
		loaded.Rows = 0
	}
	w.seq = loaded.Seq
	w.file = loaded.File
	w.rows = loaded.Rows
}

func (w *Writer) filePath(index int) string {
	return filepath.Join(w.dir, fmt.Sprintf("tx-%03d.csv", index))
}

// Emit queues one event for the writer goroutine. It is safe to call from many
// goroutines and never writes the file directly.
func (w *Writer) Emit(ev Event) {
	select {
	case <-w.quit:
	case w.events <- ev:
	}
}

// Close signals the writer to drain queued events, flush, and persist final
// state. It blocks until the background goroutine has finished.
func (w *Writer) Close() error {
	w.once.Do(func() { close(w.quit) })
	<-w.done
	return w.err
}

func (w *Writer) run() {
	defer close(w.done)
	for {
		w.drainSpill()
		select {
		case ev := <-w.events:
			if err := w.writeRow(ev); err != nil && w.err == nil {
				w.err = err
			}
		case fields := <-w.diag:
			if err := w.writeDiagLine(fields); err != nil && w.err == nil {
				w.err = err
			}
		case <-w.kick:
		case <-w.quit:
			w.drainIncoming()
			w.finish()
			return
		}
	}
}

func (w *Writer) drainIncoming() {
	for {
		w.drainSpill()
		select {
		case ev := <-w.events:
			if err := w.writeRow(ev); err != nil && w.err == nil {
				w.err = err
			}
		case fields := <-w.diag:
			if err := w.writeDiagLine(fields); err != nil && w.err == nil {
				w.err = err
			}
		case <-w.kick:
		default:
			w.drainSpill()
			if w.hasSpill() {
				continue
			}
			return
		}
	}
}

func (w *Writer) writeRow(ev Event) error {
	if w.handle == nil {
		if err := w.openCurrent(); err != nil {
			return err
		}
	}
	if w.rows >= maxRowsPerFile {
		if err := w.rotate(); err != nil {
			return err
		}
	}
	w.seq++
	if err := w.csvw.Write(w.formatRow(w.seq, ev)); err != nil {
		return err
	}
	w.csvw.Flush()
	if err := w.csvw.Error(); err != nil {
		return err
	}
	if err := w.handle.Sync(); err != nil {
		return err
	}
	w.rows++
	return w.persistState()
}

func (w *Writer) openCurrent() error {
	handle, err := os.OpenFile(w.filePath(w.file), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	info, err := handle.Stat()
	if err != nil {
		handle.Close()
		return fmt.Errorf("stat log file: %w", err)
	}
	w.handle = handle
	w.csvw = csv.NewWriter(handle)
	if info.Size() == 0 {
		if _, err := handle.Write(utf8BOM); err != nil {
			return fmt.Errorf("write bom: %w", err)
		}
		if err := w.csvw.Write(Header); err != nil {
			return err
		}
		w.csvw.Flush()
		return w.csvw.Error()
	}
	return nil
}

func (w *Writer) rotate() error {
	if w.csvw != nil {
		w.csvw.Flush()
		if err := w.csvw.Error(); err != nil {
			return err
		}
	}
	if w.handle != nil {
		if err := w.handle.Sync(); err != nil {
			return err
		}
		if err := w.handle.Close(); err != nil {
			return err
		}
	}
	w.handle = nil
	w.csvw = nil
	w.file++
	w.rows = 0
	return w.openCurrent()
}

func (w *Writer) finish() {
	if w.csvw != nil {
		w.csvw.Flush()
	}
	if w.handle != nil {
		w.handle.Sync()
		w.handle.Close()
		w.handle = nil
		w.csvw = nil
	}
	if w.diagHandle != nil {
		w.diagHandle.Sync()
		w.diagHandle.Close()
		w.diagHandle = nil
	}
	if err := w.persistState(); err != nil && w.err == nil {
		w.err = err
	}
}

func (w *Writer) formatRow(seq int, ev Event) []string {
	timestamp := ev.TsUTC.UTC().Format("2006-01-02T15:04:05Z")
	return []string{
		strconv.Itoa(seq),
		ev.TxHash,
		timestamp,
		ev.BatchID,
		ev.Mode,
		ev.Chain,
		ev.WalletFrom,
		ev.WalletTo,
		ev.Token,
		ev.TokenAddress,
		ev.AmountSent,
		ev.AmountReceived,
		ev.Nonce,
		ev.GasUsed,
		ev.FeeNative,
		string(ev.Status),
		logging.ScrubText(ev.Error),
		logging.ScrubText(ev.ExplorerURL),
	}
}

func (w *Writer) persistState() error {
	data, err := json.Marshal(state{Seq: w.seq, File: w.file, Rows: w.rows})
	if err != nil {
		return fmt.Errorf("encode log state: %w", err)
	}
	tmp := w.statePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write log state: %w", err)
	}
	if err := os.Rename(tmp, w.statePath); err != nil {
		return fmt.Errorf("commit log state: %w", err)
	}
	return nil
}
