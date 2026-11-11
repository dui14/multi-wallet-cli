package render

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout runs fn while capturing everything written to os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	done := make(chan string, 1)
	go func() {
		var buffer bytes.Buffer
		_, _ = io.Copy(&buffer, reader)
		done <- buffer.String()
	}()
	fn()
	_ = writer.Close()
	os.Stdout = original
	return stripANSI(<-done)
}

func sampleTable(width int) Table {
	return Table{
		Width: width,
		Columns: []Column{
			{Header: "Wallet", Align: AlignLeft},
			{Header: "Chain", Align: AlignLeft},
			{Header: "Native", Align: AlignRight},
			{Header: "USDC", Align: AlignRight},
			{Header: "Status", Align: AlignLeft},
		},
		Rows: []Row{
			{Cells: []string{"bnb-transfer", "arbitrum", "0.000000", "0.000000", "OK"}, First: true},
			{Cells: []string{"", "base", "0.000002", "0.000000", "OK"}},
			{Cells: []string{"eth-transfer", "polygon", "0.244003", "0.000000", "OK"}, First: true},
		},
	}
}

func TestTableRendersGridWithGroupSeparators(t *testing.T) {
	output := captureStdout(t, func() { sampleTable(120).Render() })
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) != 8 {
		t.Fatalf("expected 8 grid lines, got %d:\n%s", len(lines), output)
	}
	if !strings.HasPrefix(lines[0], " ┌") || !strings.HasSuffix(lines[0], "┐") {
		t.Fatalf("unexpected top border: %q", lines[0])
	}
	if !strings.Contains(lines[1], "Wallet") || !strings.Contains(lines[1], "Status") {
		t.Fatalf("unexpected header line: %q", lines[1])
	}
	if !strings.Contains(lines[2], "├") || !strings.Contains(lines[2], "┼") {
		t.Fatalf("unexpected header separator: %q", lines[2])
	}
	if !strings.Contains(lines[5], "├") {
		t.Fatalf("expected a group separator before the second wallet, got %q", lines[5])
	}
	if !strings.HasSuffix(lines[7], "┘") {
		t.Fatalf("unexpected bottom border: %q", lines[7])
	}
}

func TestTablePadsToWidestCell(t *testing.T) {
	output := captureStdout(t, func() { sampleTable(120).Render() })
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	widths := map[int]bool{}
	for _, line := range lines {
		widths[len([]rune(line))] = true
	}
	if len(widths) != 1 {
		t.Fatalf("expected every line to share the same width, got %v:\n%s", widths, output)
	}
}

func TestTableLeftAlignsHeaderButRightAlignsAmounts(t *testing.T) {
	table := Table{
		Width: 120,
		Columns: []Column{
			{Header: "Wallet", Align: AlignLeft},
			{Header: "Native", Align: AlignRight},
		},
		Rows: []Row{
			{Cells: []string{"w1", "1.0"}},
			{Cells: []string{"w2", "1234.56789"}},
		},
	}
	output := captureStdout(t, func() { table.Render() })
	if !strings.Contains(output, "│ Native ") {
		t.Fatalf("expected a left-aligned header, got:\n%s", output)
	}
	if !strings.Contains(output, "│        1.0 │") {
		t.Fatalf("expected 1.0 to be right-aligned, got:\n%s", output)
	}
}

func TestTableFallsBackToStackedWhenNarrow(t *testing.T) {
	output := captureStdout(t, func() { sampleTable(60).Render() })
	if strings.Contains(output, "┌") {
		t.Fatalf("expected stacked output on a narrow terminal, got:\n%s", output)
	}
	if !strings.Contains(output, "bnb-transfer · arbitrum") {
		t.Fatalf("expected the wallet and chain summary line, got:\n%s", output)
	}
	if !strings.Contains(output, "USDC:") {
		t.Fatalf("expected stacked token lines, got:\n%s", output)
	}
}

func TestStripANSILeavesPlainTextUntouched(t *testing.T) {
	if got := stripANSI("plain"); got != "plain" {
		t.Fatalf("unexpected strip result: %q", got)
	}
	if got := stripANSI("\x1b[32mok\x1b[0m"); got != "ok" {
		t.Fatalf("unexpected strip result: %q", got)
	}
}
