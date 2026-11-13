package cli

import (
	"strings"
	"testing"

	"multi-wallet-cli/infra/i18n"
)

func TestInspectPastedAddresses_PrivateKeyCancelsWithoutSecret(t *testing.T) {
	bare := strings.Repeat("a", 64)
	prefixed := "0x" + strings.Repeat("b", 64)
	warning := i18n.T("transfer.paste.secret")
	for _, secret := range []string{bare, prefixed} {
		items, message, canceled := inspectPastedAddresses(secret, "evm")
		if !canceled || message != warning || items != nil {
			t.Fatal("private key must cancel with the security warning")
		}
		if strings.Contains(message, secret) {
			t.Fatal("security warning includes the pasted secret")
		}
	}
}

func TestInspectPastedAddresses_SeedPhraseCancelsWithoutSecret(t *testing.T) {
	warning := i18n.T("transfer.paste.secret")
	for _, count := range []int{12, 24} {
		phrase := strings.TrimSpace(strings.Repeat("abandon ", count))
		items, message, canceled := inspectPastedAddresses(phrase, "evm")
		if !canceled || message != warning || items != nil {
			t.Fatal("seed phrase must cancel with the security warning")
		}
		if strings.Contains(message, "abandon") {
			t.Fatal("security warning includes the pasted secret")
		}
	}
}

func TestInspectPastedAddresses_ShortEVMIsTruncated(t *testing.T) {
	full := "0x1234567890abcdef1234567890abcdef123456"
	_, message, canceled := inspectPastedAddresses(full, "evm")
	if canceled {
		t.Fatal("format error must not use the security warning")
	}
	if strings.Contains(message, full) {
		t.Fatal("validation message includes the full address")
	}
	if !strings.Contains(message, "- 0x12...3456: ") {
		t.Fatal("address was not truncated to first 4 and last 4")
	}
	if !strings.Contains(message, i18n.T("transfer.paste.evm_short")) {
		t.Fatal("missing EVM length reason")
	}
	if !strings.HasPrefix(message, i18n.T("transfer.paste.invalid_title")) {
		t.Fatal("missing invalid-address heading")
	}
	if strings.Contains(message, "**") || strings.Contains(message, "`") {
		t.Fatal("validation message contains markdown")
	}
}

func TestInspectPastedAddresses_SolanaOnEVMIsWrongNetwork(t *testing.T) {
	full := "So11111111111111111111111111111111"
	_, message, canceled := inspectPastedAddresses(full, "evm")
	if canceled {
		t.Fatal("wrong network must not use the security warning")
	}
	if strings.Contains(message, full) {
		t.Fatal("validation message includes the full address")
	}
	if !strings.Contains(message, i18n.T("transfer.paste.wrong_network", "Solana", "EVM")) {
		t.Fatal("missing wrong-network reason")
	}
	if !strings.Contains(message, "- So11...1111: ") {
		t.Fatal("solana address was not truncated")
	}
	if strings.Contains(message, "**") || strings.Contains(message, "`") {
		t.Fatal("validation message contains markdown")
	}
}

func TestInspectPastedAddresses_ValidEVMAccepted(t *testing.T) {
	full := "0x00000000000000000000000000000000000000a1"
	items, message, canceled := inspectPastedAddresses(full, "evm")
	if canceled || message != "" || len(items) != 1 || items[0] != full {
		t.Fatal("valid EVM address must be accepted")
	}
}

func TestInspectPastedAddresses_SecretHidesOtherAddresses(t *testing.T) {
	valid := "0x00000000000000000000000000000000000000a1"
	secret := strings.Repeat("c", 64)
	_, message, canceled := inspectPastedAddresses(valid+" "+secret, "evm")
	warning := i18n.T("transfer.paste.secret")
	if !canceled || message != warning {
		t.Fatal("a secret in a mixed list must be the only result")
	}
	if strings.Contains(message, valid) || strings.Contains(message, secret) {
		t.Fatal("security warning includes pasted material")
	}
}

func TestInspectPastedAddresses_FollowsLanguage(t *testing.T) {
	previous := i18n.Language()
	t.Cleanup(func() { i18n.SetLanguage(previous) })
	full := "0x1234567890abcdef1234567890abcdef123456"
	i18n.SetLanguage("en")
	_, english, _ := inspectPastedAddresses(full, "evm")
	if !strings.Contains(english, "Wrong EVM format (not 42 characters)") {
		t.Fatal("english UI did not use the english reason")
	}
	if !strings.HasPrefix(english, "❌ Invalid address detected:") {
		t.Fatal("english UI did not use the english heading")
	}
	i18n.SetLanguage("vi")
	_, vietnamese, _ := inspectPastedAddresses(full, "evm")
	if !strings.Contains(vietnamese, "Sai định dạng EVM (không đủ 42 ký tự)") {
		t.Fatal("vietnamese UI did not use the vietnamese reason")
	}
	if !strings.HasPrefix(vietnamese, "❌ Phát hiện địa chỉ không hợp lệ:") {
		t.Fatal("vietnamese UI did not use the vietnamese heading")
	}
	if strings.Contains(english, "**") || strings.Contains(vietnamese, "`") {
		t.Fatal("validation message contains markdown")
	}
}

func TestInspectPastedAddresses_EmptySkips(t *testing.T) {
	items, message, canceled := inspectPastedAddresses("  \n ", "evm")
	if canceled || message != "" || items != nil {
		t.Fatal("empty paste must skip validation")
	}
}
