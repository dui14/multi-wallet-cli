package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

const (
	CipherAES256GCM = "aes-256-gcm"
	KDFArgon2id     = "argon2id"
)

type KDFParams struct {
	Time    uint32 `json:"time"`
	Memory  uint32 `json:"memory"`
	Threads uint8  `json:"threads"`
	KeyLen  uint32 `json:"keyLen"`
}

type Envelope struct {
	Version    int       `json:"version"`
	Cipher     string    `json:"cipher"`
	Ciphertext string    `json:"ciphertext"`
	Nonce      string    `json:"nonce"`
	Salt       string    `json:"salt"`
	KDF        string    `json:"kdf"`
	KDFParams  KDFParams `json:"kdfparams"`
}

var DefaultKDFParams = KDFParams{
	Time:    3,
	Memory:  65536,
	Threads: 4,
	KeyLen:  32,
}

func DeriveKey(password []byte, salt []byte, params KDFParams) ([]byte, error) {
	if len(password) == 0 {
		return nil, errors.New("derive key: password is empty")
	}
	if len(salt) == 0 {
		return nil, errors.New("derive key: salt is empty")
	}
	if params.KeyLen != 32 {
		return nil, fmt.Errorf("derive key: unsupported key length %d", params.KeyLen)
	}
	key := argon2.IDKey(password, salt, params.Time, params.Memory, params.Threads, params.KeyLen)
	return key, nil
}

func Seal(key []byte, plaintext []byte) ([]byte, []byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("create gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("read nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

func Open(key []byte, nonce []byte, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("open gcm: %w", err)
	}
	return plaintext, nil
}

func RandomSalt() ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("read salt: %w", err)
	}
	return salt, nil
}

func Zeroize(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
}

func HMACSHA256(key, data []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, errors.New("hmac: key is empty")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil), nil
}

func DeriveAESKey(password []byte, salt []byte, keyFile []byte, params KDFParams) ([]byte, error) {
	if len(keyFile) == 0 {
		return nil, errors.New("derive aes key: keyfile is empty")
	}
	k1, err := DeriveKey(password, salt, params)
	if err != nil {
		return nil, err
	}
	defer Zeroize(k1)
	aesKey, err := HMACSHA256(keyFile, k1)
	if err != nil {
		return nil, err
	}
	return aesKey, nil
}
