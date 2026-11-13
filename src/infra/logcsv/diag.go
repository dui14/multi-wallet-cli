package logcsv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"multi-wallet-cli/infra/logging"
)

const DiagFileName = "rpc-diag.jsonl"

const diagBuffer = 4096

var embeddedURL = regexp.MustCompile(`https?://[^\s"'<>]+`)

var longHex = regexp.MustCompile(`0x[0-9a-fA-F]{80,}`)

func (w *Writer) WriteDiag(fields map[string]any) {
	if w == nil || len(fields) == 0 {
		return
	}
	copied := make(map[string]any, len(fields))
	for key, value := range fields {
		copied[key] = value
	}
	select {
	case <-w.quit:
		return
	case w.diag <- copied:
		return
	default:
	}
	w.spillMu.Lock()
	w.spill = append(w.spill, copied)
	w.spillMu.Unlock()
	select {
	case <-w.quit:
	case w.kick <- struct{}{}:
	default:
	}
}

func (w *Writer) drainSpill() {
	w.spillMu.Lock()
	pending := w.spill
	w.spill = nil
	w.spillMu.Unlock()
	for _, fields := range pending {
		if err := w.writeDiagLine(fields); err != nil && w.err == nil {
			w.err = err
		}
	}
}

func (w *Writer) hasSpill() bool {
	w.spillMu.Lock()
	defer w.spillMu.Unlock()
	return len(w.spill) > 0
}

func (w *Writer) writeDiagLine(fields map[string]any) error {
	if w.diagHandle == nil {
		handle, err := os.OpenFile(filepath.Join(w.dir, DiagFileName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		w.diagHandle = handle
	}
	line := sanitizeDiag(fields)
	line["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(line)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := w.diagHandle.Write(data); err != nil {
		return err
	}
	return w.diagHandle.Sync()
}

func sanitizeDiag(fields map[string]any) map[string]any {
	out := make(map[string]any, len(fields)+1)
	for key, value := range fields {
		out[key] = sanitizeDiagValue(key, value)
	}
	return out
}

func sanitizeDiagValue(key string, value any) any {
	switch typed := value.(type) {
	case string:
		if isEndpointField(key) {
			return logging.MaskRPCURL(typed)
		}
		return scrubDiagText(typed)
	case []string:
		out := make([]string, len(typed))
		for i, item := range typed {
			if isEndpointField(key) {
				out[i] = logging.MaskRPCURL(item)
				continue
			}
			out[i] = scrubDiagText(item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(typed))
		for i, item := range typed {
			out[i] = sanitizeDiag(item)
		}
		return out
	case map[string]any:
		return sanitizeDiag(typed)
	default:
		return value
	}
}

func isEndpointField(key string) bool {
	switch key {
	case "endpoint", "healthy", "url":
		return true
	default:
		return false
	}
}

func scrubDiagText(text string) string {
	if text == "" {
		return ""
	}
	masked := embeddedURL.ReplaceAllStringFunc(text, func(raw string) string {
		trimmed := strings.TrimRight(raw, ".,);]")
		suffix := raw[len(trimmed):]
		if strings.Contains(trimmed, "***") || strings.Contains(trimmed, logging.Redacted) {
			return raw
		}
		return logging.MaskRPCURL(trimmed) + suffix
	})
	masked = longHex.ReplaceAllString(masked, "0x[REDACTED]")
	return logging.ScrubText(masked)
}
