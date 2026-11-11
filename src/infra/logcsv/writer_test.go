package logcsv

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func emitAndClose(t *testing.T, writer *Writer, events ...Event) {
	t.Helper()
	for _, ev := range events {
		writer.Emit(ev)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
}

func sampleEvent(hash string) Event {
	return Event{
		TxHash:         hash,
		TsUTC:          time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		BatchID:        "batch-1",
		Mode:           "transfer_1tomany",
		Chain:          "base",
		WalletFrom:     "main-wallet",
		WalletTo:       "0xabc",
		Token:          "USDC",
		TokenAddress:   "0xtoken",
		AmountSent:     "1000000",
		AmountReceived: "1000000",
		Nonce:          "7",
		GasUsed:        "21000",
		FeeNative:      "100",
		Status:         StatusSuccess,
	}
}

func readCSV(t *testing.T, path string) [][]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer file.Close()
	bom := make([]byte, 3)
	if _, err := file.Read(bom); err != nil {
		t.Fatalf("read bom: %v", err)
	}
	if bom[0] != 0xEF || bom[1] != 0xBB || bom[2] != 0xBF {
		t.Fatalf("expected UTF-8 BOM, got %v", bom)
	}
	rest, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(rest[3:]))).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	return rows
}

func TestWriterWritesBOMHeaderAndRow(t *testing.T) {
	dir := t.TempDir()
	writer, err := NewWriter(dir)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	emitAndClose(t, writer, sampleEvent("0xdead"))

	rows := readCSV(t, filepath.Join(dir, "tx-001.csv"))
	if len(rows) != 2 {
		t.Fatalf("expected header + 1 row, got %d", len(rows))
	}
	for index, column := range Header {
		if rows[0][index] != column {
			t.Fatalf("header column %d expected %q got %q", index, column, rows[0][index])
		}
	}
	if rows[1][0] != "1" {
		t.Fatalf("expected seq 1, got %q", rows[1][0])
	}
	if rows[1][1] != "0xdead" {
		t.Fatalf("expected tx hash column, got %q", rows[1][1])
	}
	if rows[1][2] != "2026-01-02T03:04:05Z" {
		t.Fatalf("expected utc timestamp, got %q", rows[1][2])
	}
}

func TestWriterRotatesAndKeepsGlobalSeq(t *testing.T) {
	original := maxRowsPerFile
	maxRowsPerFile = 2
	defer func() { maxRowsPerFile = original }()

	dir := t.TempDir()
	writer, err := NewWriter(dir)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	emitAndClose(t, writer, sampleEvent("0x1"), sampleEvent("0x2"), sampleEvent("0x3"), sampleEvent("0x4"), sampleEvent("0x5"))

	first := readCSV(t, filepath.Join(dir, "tx-001.csv"))
	second := readCSV(t, filepath.Join(dir, "tx-002.csv"))
	third := readCSV(t, filepath.Join(dir, "tx-003.csv"))
	if len(first) != 3 || len(second) != 3 || len(third) != 2 {
		t.Fatalf("unexpected row counts: %d %d %d", len(first), len(second), len(third))
	}
	if first[1][0] != "1" || first[2][0] != "2" {
		t.Fatalf("file 1 seq wrong: %q %q", first[1][0], first[2][0])
	}
	if second[1][0] != "3" || second[2][0] != "4" {
		t.Fatalf("file 2 seq wrong: %q %q", second[1][0], second[2][0])
	}
	if third[1][0] != "5" {
		t.Fatalf("file 3 seq wrong: %q", third[1][0])
	}
	for _, column := range Header {
		if second[0][0] != column {
			break
		}
	}
}

func TestWriterResumesGlobalSeqFromState(t *testing.T) {
	original := maxRowsPerFile
	maxRowsPerFile = 2
	defer func() { maxRowsPerFile = original }()

	dir := t.TempDir()
	first, err := NewWriter(dir)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	emitAndClose(t, first, sampleEvent("0x1"), sampleEvent("0x2"), sampleEvent("0x3"))

	second, err := NewWriter(dir)
	if err != nil {
		t.Fatalf("resume writer: %v", err)
	}
	emitAndClose(t, second, sampleEvent("0x4"))

	rows := readCSV(t, filepath.Join(dir, "tx-002.csv"))
	last := rows[len(rows)-1]
	if last[0] != "4" {
		t.Fatalf("expected resumed seq 4, got %q", last[0])
	}
	if last[1] != "0x4" {
		t.Fatalf("expected tx 0x4 in resumed file, got %q", last[1])
	}
}

func TestWriterKeepsNormalizedErrorCodeAndScrubsQueryKey(t *testing.T) {
	dir := t.TempDir()
	writer, err := NewWriter(dir)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	ev := sampleEvent("0x1")
	ev.Error = ErrRPCTimeout
	ev.ExplorerURL = "https://scan.example/tx/0x1?apikey=supersecretvalue"
	emitAndClose(t, writer, ev)

	rows := readCSV(t, filepath.Join(dir, "tx-001.csv"))
	if rows[1][16] != ErrRPCTimeout {
		t.Fatalf("expected normalized error code, got %q", rows[1][16])
	}
	if strings.Contains(rows[1][17], "supersecretvalue") {
		t.Fatalf("api key leaked into explorer column: %q", rows[1][17])
	}
}
