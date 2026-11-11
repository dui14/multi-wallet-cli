package errcodes

import "errors"

var (
	ErrInvalidInput      = errors.New("invalid input")
	ErrIncorrectPassword = errors.New("incorrect password")
	ErrNoWallets         = errors.New("no wallets found")
	ErrDuplicateLabel    = errors.New("duplicate wallet label")
	ErrDuplicateAddress  = errors.New("duplicate wallet address")
	ErrWalletCorrupted   = errors.New("wallet keystore corrupted")
	ErrUnsupportedKind   = errors.New("unsupported wallet kind")
	ErrInterrupted       = errors.New("operation interrupted")
)
