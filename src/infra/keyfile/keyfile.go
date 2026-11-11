package keyfile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

const (
	fileName   = "keyfile.bin"
	keySize    = 32
	appName    = "multi-wallet-cli"
	envAppData = "LOCALAPPDATA"
	envXDG     = "XDG_CONFIG_HOME"
)

// Dir returns the platform-specific directory where the keyfile lives outside
// the repository. On Windows it is %LOCALAPPDATA%\multi-wallet-cli, on macOS
// it is ~/Library/Application Support/multi-wallet-cli, and on Linux it is
// $XDG_CONFIG_HOME/multi-wallet-cli (or ~/.config/multi-wallet-cli).
func Dir() (string, error) {
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv(envAppData)
		if base == "" {
			base = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
		}
		return filepath.Join(base, appName), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("keyfile dir: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support", appName), nil
	default:
		base := os.Getenv(envXDG)
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("keyfile dir: %w", err)
			}
			base = filepath.Join(home, ".config")
		}
		return filepath.Join(base, appName), nil
	}
}

// Path returns the full path to the keyfile.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

// Exists reports whether the keyfile already exists on disk.
func Exists() (bool, error) {
	path, err := Path()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Load reads the keyfile from disk and returns its raw bytes.
func Load() ([]byte, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read keyfile: %w", err)
	}
	if len(data) != keySize {
		return nil, fmt.Errorf("keyfile must be %d bytes, got %d", keySize, len(data))
	}
	return data, nil
}

// Create generates a new keyfile with keySize random bytes and writes it with
// restrictive permissions (0o600). It is safe to call when the file does not
// yet exist; if it exists, an error is returned to avoid silent overwrite.
func Create() ([]byte, error) {
	exists, err := Exists()
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errors.New("keyfile already exists")
	}
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create keyfile directory: %w", err)
	}
	key := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate keyfile: %w", err)
	}
	path, err := Path()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("write keyfile: %w", err)
	}
	return key, nil
}

// Ensure loads the existing keyfile or creates a new one if absent. Returns the
// keyfile bytes and whether it was newly created.
func Ensure() ([]byte, bool, error) {
	exists, err := Exists()
	if err != nil {
		return nil, false, err
	}
	if exists {
		key, err := Load()
		if err != nil {
			return nil, false, err
		}
		return key, false, nil
	}
	key, err := Create()
	if err != nil {
		return nil, false, err
	}
	return key, true, nil
}

// Hex returns the lowercase hex encoding of the keyfile bytes.
func Hex(b []byte) string {
	return hex.EncodeToString(b)
}
