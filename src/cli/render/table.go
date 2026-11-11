package render

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"

	"multi-wallet-cli/cli/theme"
)

// defaultWidth is used when the terminal size cannot be detected.
const defaultWidth = 80

// minGridWidth is the narrowest terminal that still fits a bordered grid.
const minGridWidth = 80

const escapeByte = '\x1b'

const (
	topLeft     = "┌"
	topJoin     = "┬"
	topRight    = "┐"
	midLeft     = "├"
	midJoin     = "┼"
	midRight    = "┤"
	bottomLeft  = "└"
	bottomJoin  = "┴"
	bottomRight = "┘"
	horizontal  = "─"
	vertical    = "│"
)

// Align selects how a column pads its cells.
type Align int

// Supported column alignments.
const (
	AlignLeft Align = iota
	AlignRight
)

// Column describes one table column.
type Column struct {
	Header string
	Align  Align
}

// Row is one rendered table row. First marks the first row of a group so the
// renderer draws a separator rule above it.
type Row struct {
	Cells []string
	First bool
}

// Table renders an aligned, bordered table sized to the terminal width.
type Table struct {
	Columns []Column
	Rows    []Row
	Width   int
}

// TerminalWidth returns the current terminal width, or a safe default when the
// size cannot be detected (for example when stdout is not a terminal).
func TerminalWidth() int {
	if width, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && width > 0 {
		return width
	}
	return defaultWidth
}

// Render prints the table. It prints an aligned grid when the terminal is wide
// enough and falls back to stacked key/value lines when it is narrow.
func (t Table) Render() {
	if len(t.Columns) == 0 {
		return
	}
	width := t.Width
	if width <= 0 {
		width = TerminalWidth()
	}
	if width < minGridWidth {
		t.renderStacked()
		return
	}
	t.renderGrid()
}

func (t Table) renderGrid() {
	widths := t.columnWidths()
	fmt.Println(borderLine(topLeft, topJoin, topRight, widths))
	fmt.Println(leftDataLine(t.headerCells(), widths))
	fmt.Println(borderLine(midLeft, midJoin, midRight, widths))
	for index, row := range t.Rows {
		if index > 0 && row.First {
			fmt.Println(borderLine(midLeft, midJoin, midRight, widths))
		}
		fmt.Println(t.dataLine(row.Cells, widths))
	}
	fmt.Println(borderLine(bottomLeft, bottomJoin, bottomRight, widths))
}

func (t Table) renderStacked() {
	for index, row := range t.Rows {
		if index > 0 {
			fmt.Println()
		}
		head := strings.TrimSpace(cellAt(row.Cells, 0))
		chain := strings.TrimSpace(cellAt(row.Cells, 1))
		if head == "" {
			fmt.Println("  " + theme.Value(chain))
		} else if chain == "" {
			fmt.Println(theme.Value(head))
		} else {
			fmt.Println(theme.Value(head) + " · " + theme.Value(chain))
		}
		for column := 2; column < len(t.Columns); column++ {
			label := strings.TrimSpace(t.Columns[column].Header)
			value := strings.TrimSpace(cellAt(row.Cells, column))
			if value == "" {
				continue
			}
			fmt.Println("  " + theme.Label(label+": ") + value)
		}
	}
}

func (t Table) headerCells() []string {
	cells := make([]string, len(t.Columns))
	for index, column := range t.Columns {
		cells[index] = theme.Label(column.Header)
	}
	return cells
}

func (t Table) columnWidths() []int {
	widths := make([]int, len(t.Columns))
	for index, column := range t.Columns {
		widths[index] = displayWidth(column.Header)
	}
	for _, row := range t.Rows {
		for index := range t.Columns {
			if width := displayWidth(cellAt(row.Cells, index)); width > widths[index] {
				widths[index] = width
			}
		}
	}
	return widths
}

func borderLine(left, join, right string, widths []int) string {
	var builder strings.Builder
	builder.WriteString(" " + left)
	for index, width := range widths {
		if index > 0 {
			builder.WriteString(join)
		}
		builder.WriteString(strings.Repeat(horizontal, width+2))
	}
	builder.WriteString(right)
	return builder.String()
}

func (t Table) dataLine(cells []string, widths []int) string {
	var builder strings.Builder
	builder.WriteString(" " + vertical)
	for index, width := range widths {
		builder.WriteString(" ")
		builder.WriteString(padCell(cellAt(cells, index), width, t.alignAt(index)))
		builder.WriteString(" " + vertical)
	}
	return builder.String()
}

func leftDataLine(cells []string, widths []int) string {
	var builder strings.Builder
	builder.WriteString(" " + vertical)
	for index, width := range widths {
		builder.WriteString(" ")
		builder.WriteString(padCell(cellAt(cells, index), width, AlignLeft))
		builder.WriteString(" " + vertical)
	}
	return builder.String()
}

func (t Table) alignAt(index int) Align {
	if index < 0 || index >= len(t.Columns) {
		return AlignLeft
	}
	return t.Columns[index].Align
}

func padCell(cell string, width int, align Align) string {
	padding := width - displayWidth(cell)
	if padding < 0 {
		padding = 0
	}
	if align == AlignRight {
		return strings.Repeat(" ", padding) + cell
	}
	return cell + strings.Repeat(" ", padding)
}

func cellAt(cells []string, index int) string {
	if index < 0 || index >= len(cells) {
		return ""
	}
	return cells[index]
}

func displayWidth(value string) int {
	return utf8.RuneCountInString(stripANSI(value))
}

// stripANSI removes SGR escape sequences so padding measures the visible text.
func stripANSI(value string) string {
	if !strings.ContainsRune(value, escapeByte) {
		return value
	}
	var builder strings.Builder
	for index := 0; index < len(value); {
		if value[index] == escapeByte {
			index++
			if index < len(value) && value[index] == '[' {
				index++
				for index < len(value) && !(value[index] >= '@' && value[index] <= '~') {
					index++
				}
				if index < len(value) {
					index++
				}
			}
			continue
		}
		builder.WriteByte(value[index])
		index++
	}
	return builder.String()
}
