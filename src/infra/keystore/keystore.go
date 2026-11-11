package keystore

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"multi-wallet-cli/domain/errcodes"
	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/config"
	"multi-wallet-cli/infra/crypto"
)

type WalletFile struct {
	Address   string           `json:"address"`
	Label     string           `json:"label"`
	ChainKind wallet.ChainKind `json:"chainKind"`
	Crypto    crypto.Envelope  `json:"crypto"`
}

type AgentConfigFile struct {
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Crypto   crypto.Envelope `json:"crypto"`
}

type SaveWalletRequest struct {
	Address    string
	Label      string
	ChainKind  wallet.ChainKind
	PrivateKey []byte
	Password   []byte
	KeyFile    []byte
}

type SaveAgentConfigRequest struct {
	Provider string
	Model    string
	APIKey   []byte
	Password []byte
}

type Repository struct {
	walletDir string
	configDir string
}

func NewRepository(baseDir string) *Repository {
	return &Repository{
		walletDir: filepath.Join(baseDir, "wallets"),
		configDir: filepath.Join(baseDir, "config"),
	}
}

func (r *Repository) HasWallets(ctx context.Context) (bool, error) {
	files, err := r.WalletFiles(ctx)
	if err != nil {
		return false, err
	}
	return len(files) > 0, nil
}

func (r *Repository) WalletFiles(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	files, err := filepath.Glob(filepath.Join(r.walletDir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("list wallet files: %w", err)
	}
	sort.Strings(files)
	return files, nil
}

func (r *Repository) LoadMetadata(ctx context.Context) ([]WalletFile, error) {
	files, err := r.WalletFiles(ctx)
	if err != nil {
		return nil, err
	}
	metadata := make([]WalletFile, 0, len(files))
	for _, file := range files {
		item, err := r.readWalletFile(ctx, file)
		if err != nil {
			return nil, err
		}
		metadata = append(metadata, item)
	}
	return metadata, nil
}

func (r *Repository) SaveWallet(ctx context.Context, req SaveWalletRequest) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(r.walletDir, 0o700); err != nil {
		return "", fmt.Errorf("create wallets directory: %w", err)
	}
	salt, err := crypto.RandomSalt()
	if err != nil {
		return "", err
	}
	key, err := crypto.DeriveAESKey(req.Password, salt, req.KeyFile, crypto.DefaultKDFParams)
	if err != nil {
		return "", err
	}
	defer crypto.Zeroize(key)
	ciphertext, nonce, err := crypto.Seal(key, req.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("encrypt wallet: %w", err)
	}
	file := WalletFile{
		Address:   req.Address,
		Label:     req.Label,
		ChainKind: req.ChainKind,
		Crypto: crypto.Envelope{
			Version:    2,
			Cipher:     crypto.CipherAES256GCM,
			Ciphertext: hex.EncodeToString(ciphertext),
			Nonce:      hex.EncodeToString(nonce),
			Salt:       hex.EncodeToString(salt),
			KDF:        crypto.KDFArgon2id,
			KDFParams:  crypto.DefaultKDFParams,
		},
	}
	path := filepath.Join(r.walletDir, req.Label+".json")
	if err := writeJSONAtomic(path, file); err != nil {
		return "", fmt.Errorf("write wallet file: %w", err)
	}
	return path, nil
}

func (r *Repository) OpenWallet(ctx context.Context, file string, password []byte, keyFile []byte) (*wallet.Wallet, error) {
	item, err := r.readWalletFile(ctx, file)
	if err != nil {
		return nil, err
	}
	plaintext, err := openEnvelopeWithKeyFile(password, keyFile, item.Crypto)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", errcodes.ErrIncorrectPassword, filepath.Base(file))
	}
	defer crypto.Zeroize(plaintext)
	return wallet.New(item.Address, item.Label, item.ChainKind, plaintext), nil
}

func (r *Repository) SaveAgentConfig(ctx context.Context, req SaveAgentConfigRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(r.configDir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	salt, err := crypto.RandomSalt()
	if err != nil {
		return err
	}
	key, err := crypto.DeriveKey(req.Password, salt, crypto.DefaultKDFParams)
	if err != nil {
		return err
	}
	defer crypto.Zeroize(key)
	ciphertext, nonce, err := crypto.Seal(key, req.APIKey)
	if err != nil {
		return fmt.Errorf("encrypt agent api key: %w", err)
	}
	file := AgentConfigFile{
		Provider: req.Provider,
		Model:    req.Model,
		Crypto: crypto.Envelope{
			Cipher:     crypto.CipherAES256GCM,
			Ciphertext: hex.EncodeToString(ciphertext),
			Nonce:      hex.EncodeToString(nonce),
			Salt:       hex.EncodeToString(salt),
			KDF:        crypto.KDFArgon2id,
			KDFParams:  crypto.DefaultKDFParams,
		},
	}
	return writeJSONAtomic(filepath.Join(r.configDir, "agent_config.json"), file)
}

func (r *Repository) DeleteAgentConfig(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(r.configDir, "agent_config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete agent config: %w", err)
	}
	return nil
}

func (r *Repository) SaveLanguage(ctx context.Context, language string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(r.configDir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	return writeJSONAtomic(filepath.Join(r.configDir, "language.json"), struct {
		Language string `json:"language"`
	}{Language: language})
}

func (r *Repository) LoadLanguage(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(r.configDir, "language.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "en", nil
	}
	if err != nil {
		return "", fmt.Errorf("read language: %w", err)
	}
	var file struct {
		Language string `json:"language"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return "", fmt.Errorf("decode language: %w", err)
	}
	return file.Language, nil
}

func (r *Repository) LoadAgentConfig(ctx context.Context, password []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path := filepath.Join(r.configDir, "agent_config.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read agent config: %w", err)
	}
	var file AgentConfigFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("decode agent config: %w", err)
	}
	apiKey, err := openEnvelope(password, file.Crypto)
	if err != nil {
		return fmt.Errorf("decrypt agent config: %w", errcodes.ErrIncorrectPassword)
	}
	config.CurrentAgentConfig = config.AgentConfig{
		Provider: file.Provider,
		Model:    file.Model,
		APIKey:   string(apiKey),
	}
	crypto.Zeroize(apiKey)
	return nil
}

func (r *Repository) readWalletFile(ctx context.Context, file string) (WalletFile, error) {
	if err := ctx.Err(); err != nil {
		return WalletFile{}, err
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return WalletFile{}, fmt.Errorf("read wallet file %s: %w", filepath.Base(file), err)
	}
	var item WalletFile
	if err := json.Unmarshal(data, &item); err != nil {
		return WalletFile{}, fmt.Errorf("%w: %s", errcodes.ErrWalletCorrupted, filepath.Base(file))
	}
	if item.Address == "" || item.Label == "" || item.ChainKind == "" || item.Crypto.Ciphertext == "" {
		return WalletFile{}, fmt.Errorf("%w: %s", errcodes.ErrWalletCorrupted, filepath.Base(file))
	}
	return item, nil
}

func openEnvelope(password []byte, envelope crypto.Envelope) ([]byte, error) {
	if envelope.Cipher != crypto.CipherAES256GCM || envelope.KDF != crypto.KDFArgon2id {
		return nil, errcodes.ErrWalletCorrupted
	}
	salt, err := hex.DecodeString(strings.TrimSpace(envelope.Salt))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid salt", errcodes.ErrWalletCorrupted)
	}
	nonce, err := hex.DecodeString(strings.TrimSpace(envelope.Nonce))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid nonce", errcodes.ErrWalletCorrupted)
	}
	ciphertext, err := hex.DecodeString(strings.TrimSpace(envelope.Ciphertext))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid ciphertext", errcodes.ErrWalletCorrupted)
	}
	key, err := crypto.DeriveKey(password, salt, envelope.KDFParams)
	if err != nil {
		return nil, err
	}
	defer crypto.Zeroize(key)
	plaintext, err := crypto.Open(key, nonce, ciphertext)
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}

func openEnvelopeWithKeyFile(password []byte, keyFile []byte, envelope crypto.Envelope) ([]byte, error) {
	if envelope.Cipher != crypto.CipherAES256GCM || envelope.KDF != crypto.KDFArgon2id {
		return nil, errcodes.ErrWalletCorrupted
	}
	if len(keyFile) == 0 {
		return nil, fmt.Errorf("%w: keyfile is required", errcodes.ErrIncorrectPassword)
	}
	salt, err := hex.DecodeString(strings.TrimSpace(envelope.Salt))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid salt", errcodes.ErrWalletCorrupted)
	}
	nonce, err := hex.DecodeString(strings.TrimSpace(envelope.Nonce))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid nonce", errcodes.ErrWalletCorrupted)
	}
	ciphertext, err := hex.DecodeString(strings.TrimSpace(envelope.Ciphertext))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid ciphertext", errcodes.ErrWalletCorrupted)
	}
	key, err := crypto.DeriveAESKey(password, salt, keyFile, envelope.KDFParams)
	if err != nil {
		return nil, err
	}
	defer crypto.Zeroize(key)
	plaintext, err := crypto.Open(key, nonce, ciphertext)
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".wallet-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	cleanup = false
	return nil
}
