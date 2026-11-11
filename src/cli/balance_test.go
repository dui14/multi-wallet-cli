package cli

import (
	"math/big"
	"testing"

	"github.com/fatih/color"

	balanceapp "multi-wallet-cli/app/balance"
)

func TestMain(m *testing.M) {
	color.NoColor = true
	m.Run()
}

func rawUnits(value int64) *big.Int { return big.NewInt(value) }

func TestBuildBalanceTablePivotsTokensIntoColumns(t *testing.T) {
	report := balanceapp.Report{Rows: []balanceapp.Row{
		{Wallet: "main-wallet", Chain: "arbitrum", Token: "ETH", Native: true, Raw: rawUnits(209_000_000_000_000), Decimals: 18},
		{Wallet: "main-wallet", Chain: "arbitrum", Token: "USDC", Raw: rawUnits(10), Decimals: 6},
		{Wallet: "main-wallet", Chain: "base", Token: "ETH", Native: true, Raw: rawUnits(88_000_000_000_000), Decimals: 18},
		{Wallet: "main-solana", Chain: "solana", Token: "SOL", Native: true, Raw: rawUnits(23_096_000), Decimals: 9},
	}}
	table := buildBalanceTable(report)

	headers := make([]string, 0, len(table.Columns))
	for _, column := range table.Columns {
		headers = append(headers, column.Header)
	}
	expected := []string{"Wallet", "Chain", "Native", "USDC", "Status"}
	if len(headers) != len(expected) {
		t.Fatalf("expected headers %v, got %v", expected, headers)
	}
	for index := range expected {
		if headers[index] != expected[index] {
			t.Fatalf("expected headers %v, got %v", expected, headers)
		}
	}
	if len(table.Rows) != 3 {
		t.Fatalf("expected three wallet-chain rows, got %d", len(table.Rows))
	}
	if table.Rows[0].Cells[0] != "main-wallet" {
		t.Fatalf("expected the wallet label on the first group row, got %q", table.Rows[0].Cells[0])
	}
	if table.Rows[1].Cells[0] != "" {
		t.Fatalf("expected a blank wallet cell on the second group row, got %q", table.Rows[1].Cells[0])
	}
	if table.Rows[1].First {
		t.Fatal("expected the same-wallet chain row not to start a new group")
	}
	if !table.Rows[2].First {
		t.Fatal("expected the next wallet to start a new group")
	}
}

func TestBuildBalanceTableMarksNotApplicableAsDash(t *testing.T) {
	report := balanceapp.Report{Rows: []balanceapp.Row{
		{Wallet: "main-solana", Chain: "solana", Token: "SOL", Native: true, Raw: rawUnits(1), Decimals: 9},
	}}
	table := buildBalanceTable(report)
	headers := make([]string, 0, len(table.Columns))
	for _, column := range table.Columns {
		headers = append(headers, column.Header)
	}
	expected := []string{"Wallet", "Chain", "Native", "Status"}
	if len(headers) != len(expected) {
		t.Fatalf("expected headers %v, got %v", expected, headers)
	}
}

func TestBuildBalanceTableCollapsesChainErrors(t *testing.T) {
	report := balanceapp.Report{Rows: []balanceapp.Row{
		{Wallet: "w1", Chain: "base", Token: "ETH", Native: true, Raw: rawUnits(1), Decimals: 18},
		{Wallet: "w1", Chain: "polygon", Token: balanceapp.RowErrorToken, Err: testError("rpc down")},
	}}
	table := buildBalanceTable(report)
	if len(table.Rows) != 2 {
		t.Fatalf("expected two rows, got %d", len(table.Rows))
	}
	last := table.Rows[1].Cells[len(table.Rows[1].Cells)-1]
	if last != "ERROR" {
		t.Fatalf("expected the chain error to collapse into the status cell, got %q", last)
	}
}

type testError string

func (e testError) Error() string { return string(e) }
