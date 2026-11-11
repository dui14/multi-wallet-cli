package keystore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"multi-wallet-cli/domain/wallet"
)

func TestSaveWallet_UsesLabelAsFilename(t *testing.T) {
	baseDir := t.TempDir()
	repository := NewRepository(baseDir)
	password := []byte("correct horse battery")
	privateKey := []byte("private-key")
	keyFile := make([]byte, 32)

	path, err := repository.SaveWallet(context.Background(), SaveWalletRequest{
		Address:    "0x123",
		Label:      "treasury",
		ChainKind:  wallet.ChainKindEVM,
		PrivateKey: privateKey,
		Password:   password,
		KeyFile:    keyFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "treasury.json" {
		t.Fatalf("expected treasury.json, got %s", filepath.Base(path))
	}
	if _, err := os.Stat(filepath.Join(baseDir, "wallets", "treasury.json")); err != nil {
		t.Fatalf("expected label-named wallet file: %v", err)
	}
	files, err := repository.WalletFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected one wallet file, got %d", len(files))
	}
}
