package walletsvc

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	gethcrypto "github.com/ethereum/go-ethereum/crypto"
	solana "github.com/gagliardetto/solana-go"
	"golang.org/x/sync/errgroup"

	"multi-wallet-cli/domain/errcodes"
	"multi-wallet-cli/domain/wallet"
	"multi-wallet-cli/infra/keystore"
)

type Repository interface {
	HasWallets(ctx context.Context) (bool, error)
	WalletFiles(ctx context.Context) ([]string, error)
	LoadMetadata(ctx context.Context) ([]keystore.WalletFile, error)
	SaveWallet(ctx context.Context, req keystore.SaveWalletRequest) (string, error)
	OpenWallet(ctx context.Context, file string, password []byte, keyFile []byte) (*wallet.Wallet, error)
	LoadAgentConfig(ctx context.Context, password []byte) error
	SaveAgentConfig(ctx context.Context, req keystore.SaveAgentConfigRequest) error
	SaveLanguage(ctx context.Context, language string) error
}

type Service struct {
	repo Repository
	set  *wallet.WalletSet
}

type ImportWalletRequest struct {
	PrivateKey []byte
	ChainKind  wallet.ChainKind
	Label      string
	Password   []byte
	KeyFile    []byte
}

type ImportWalletResult struct {
	Address   string
	Label     string
	ChainKind wallet.ChainKind
	File      string
}

func New(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) HasWallets(ctx context.Context) (bool, error) {
	return s.repo.HasWallets(ctx)
}

func (s *Service) ExistingMetadata(ctx context.Context) ([]keystore.WalletFile, error) {
	return s.repo.LoadMetadata(ctx)
}

func (s *Service) WalletFileCount(ctx context.Context) (int, error) {
	files, err := s.repo.WalletFiles(ctx)
	if err != nil {
		return 0, err
	}
	return len(files), nil
}

func (s *Service) ImportWallet(ctx context.Context, req ImportWalletRequest) (ImportWalletResult, error) {
	if err := ValidateMasterPassword(req.Password); err != nil {
		return ImportWalletResult{}, err
	}
	label, err := validateLabel(req.Label)
	if err != nil {
		return ImportWalletResult{}, err
	}
	privateKey, address, kind, err := normalizePrivateKey(req.PrivateKey, req.ChainKind)
	if err != nil {
		return ImportWalletResult{}, err
	}
	defer zeroize(privateKey)
	if err := s.ensureUnique(ctx, label, address); err != nil {
		return ImportWalletResult{}, err
	}
	path, err := s.repo.SaveWallet(ctx, keystore.SaveWalletRequest{
		Address:    address,
		Label:      label,
		ChainKind:  kind,
		PrivateKey: privateKey,
		Password:   req.Password,
		KeyFile:    req.KeyFile,
	})
	if err != nil {
		return ImportWalletResult{}, fmt.Errorf("save wallet: %w", err)
	}
	return ImportWalletResult{
		Address:   address,
		Label:     label,
		ChainKind: kind,
		File:      filepath.Base(path),
	}, nil
}

func (s *Service) UnlockAll(ctx context.Context, password []byte, keyFile []byte) (*wallet.WalletSet, error) {
	if len(password) == 0 {
		return nil, fmt.Errorf("%w: password is required", errcodes.ErrInvalidInput)
	}
	if len(keyFile) == 0 {
		return nil, fmt.Errorf("%w: keyfile is required", errcodes.ErrInvalidInput)
	}
	files, err := s.repo.WalletFiles(ctx)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errcodes.ErrNoWallets
	}
	items := make([]*wallet.Wallet, 0, len(files))
	failures := make([]wallet.UnlockFailure, 0)
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(4)
	for _, file := range files {
		file := file
		g.Go(func() error {
			item, err := s.repo.OpenWallet(gctx, file, password, keyFile)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures = append(failures, wallet.UnlockFailure{File: filepath.Base(file), Err: err})
				return nil
			}
			items = append(items, item)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("unlock wallets: %w", err)
	}
	if len(items) == 0 && len(failures) > 0 {
		for _, failure := range failures {
			if !errors.Is(failure.Err, errcodes.ErrIncorrectPassword) {
				return nil, errcodes.ErrWalletCorrupted
			}
		}
		return nil, errcodes.ErrIncorrectPassword
	}
	set := wallet.NewSet(items, failures)
	if err := s.repo.LoadAgentConfig(ctx, password); err != nil {
		set.Zeroize()
		return nil, err
	}
	s.set = set
	return set, nil
}

func (s *Service) Wallets() *wallet.WalletSet {
	return s.set
}

func (s *Service) Zeroize() {
	if s == nil || s.set == nil {
		return
	}
	s.set.Zeroize()
}

func ValidateMasterPassword(password []byte) error {
	if len(password) < 8 {
		return fmt.Errorf("%w: master password must be at least 8 characters", errcodes.ErrInvalidInput)
	}
	return nil
}

func DetectPrivateKeyKind(input []byte) (wallet.ChainKind, string, error) {
	key, address, kind, err := normalizePrivateKey(input, "")
	if err != nil {
		return "", "", err
	}
	zeroize(key)
	return kind, address, nil
}

func (s *Service) ensureUnique(ctx context.Context, label string, address string) error {
	metadata, err := s.repo.LoadMetadata(ctx)
	if err != nil {
		return err
	}
	for _, item := range metadata {
		if strings.EqualFold(item.Label, label) {
			return errcodes.ErrDuplicateLabel
		}
		if item.Address == address || strings.EqualFold(item.Address, address) {
			return errcodes.ErrDuplicateAddress
		}
	}
	return nil
}

func validateLabel(label string) (string, error) {
	trimmed := strings.TrimSpace(label)
	if trimmed == "" {
		return "", fmt.Errorf("%w: label is required", errcodes.ErrInvalidInput)
	}
	if len(trimmed) > 64 {
		return "", fmt.Errorf("%w: label must be 64 characters or fewer", errcodes.ErrInvalidInput)
	}
	if strings.ContainsAny(trimmed, `/\\`) || trimmed == "." || trimmed == ".." {
		return "", fmt.Errorf("%w: label cannot contain path separators", errcodes.ErrInvalidInput)
	}
	for _, r := range trimmed {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: label cannot contain control characters", errcodes.ErrInvalidInput)
		}
	}
	return trimmed, nil
}

func normalizePrivateKey(input []byte, requested wallet.ChainKind) ([]byte, string, wallet.ChainKind, error) {
	raw := bytes.TrimSpace(input)
	if len(raw) == 0 {
		return nil, "", "", fmt.Errorf("%w: private key is required", errcodes.ErrInvalidInput)
	}
	if requested == "" || requested == wallet.ChainKindEVM {
		key, address, err := normalizeEVMKey(raw)
		if err == nil {
			return key, address, wallet.ChainKindEVM, nil
		}
		if requested == wallet.ChainKindEVM {
			return nil, "", "", err
		}
	}
	if requested == "" || requested == wallet.ChainKindSolana {
		key, address, err := normalizeSolanaKey(raw)
		if err == nil {
			return key, address, wallet.ChainKindSolana, nil
		}
		if requested == wallet.ChainKindSolana {
			return nil, "", "", err
		}
	}
	if requested != "" {
		return nil, "", "", errcodes.ErrUnsupportedKind
	}
	return nil, "", "", fmt.Errorf("%w: private key format is not recognized", errcodes.ErrInvalidInput)
}

func normalizeEVMKey(raw []byte) ([]byte, string, error) {
	trimmed := raw
	if len(trimmed) >= 2 && trimmed[0] == '0' && (trimmed[1] == 'x' || trimmed[1] == 'X') {
		trimmed = trimmed[2:]
	}
	if len(trimmed) != 64 {
		return nil, "", fmt.Errorf("%w: EVM private key must be 32-byte hex", errcodes.ErrInvalidInput)
	}
	keyBytes := make([]byte, 32)
	if _, err := hex.Decode(keyBytes, trimmed); err != nil {
		return nil, "", fmt.Errorf("%w: EVM private key must be valid hex", errcodes.ErrInvalidInput)
	}
	privateKey, err := gethcrypto.ToECDSA(keyBytes)
	if err != nil {
		zeroize(keyBytes)
		return nil, "", fmt.Errorf("%w: invalid EVM private key", errcodes.ErrInvalidInput)
	}
	address := gethcrypto.PubkeyToAddress(privateKey.PublicKey).Hex()
	return keyBytes, address, nil
}

func normalizeSolanaKey(raw []byte) ([]byte, string, error) {
	privateKey, err := solana.PrivateKeyFromBase58(string(raw))
	if err != nil {
		return nil, "", fmt.Errorf("%w: invalid Solana private key", errcodes.ErrInvalidInput)
	}
	if len(privateKey) != 64 {
		return nil, "", fmt.Errorf("%w: Solana private key must be 64 bytes", errcodes.ErrInvalidInput)
	}
	address := privateKey.PublicKey().String()
	key := make([]byte, len(privateKey))
	copy(key, privateKey)
	return key, address, nil
}

func zeroize(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
}

func UserMessage(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errcodes.ErrNoWallets):
		return "No wallets found. Run `go run main.go -setup` to add one."
	case errors.Is(err, errcodes.ErrIncorrectPassword):
		return "Incorrect password. Try again, or use your recovery backup if you have forgotten it."
	case errors.Is(err, errcodes.ErrDuplicateLabel):
		return "That wallet label already exists. Use a different label."
	case errors.Is(err, errcodes.ErrDuplicateAddress):
		return "That wallet address is already stored."
	case errors.Is(err, errcodes.ErrInvalidInput):
		return strings.TrimPrefix(err.Error(), errcodes.ErrInvalidInput.Error()+": ")
	case errors.Is(err, errcodes.ErrWalletCorrupted):
		return "A wallet file could not be read. Check the wallets directory and try again."
	default:
		return "Something went wrong. Check the input and try again."
	}
}
