package relayexec

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// ParseQuantity converts a Relay decimal string into a base-unit big.Int,
// tolerating empty input as zero.
func ParseQuantity(value string) *big.Int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return big.NewInt(0)
	}
	parsed, ok := new(big.Int).SetString(trimmed, 0)
	if !ok {
		return big.NewInt(0)
	}
	return parsed
}

// ParseQuantityUint converts a Relay decimal string into a uint64, zero on any
// parse failure so a missing gas hint never aborts a step.
func ParseQuantityUint(value string) uint64 {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	parsed, err := strconv.ParseUint(trimmed, 0, 64)
	if err != nil {
		return 0
	}
	return parsed
}

// DecodeHex turns a 0x-prefixed calldata string into bytes.
func DecodeHex(value string) []byte {
	trimmed := strings.TrimPrefix(strings.TrimSpace(value), "0x")
	if trimmed == "" {
		return nil
	}
	decoded, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil
	}
	return decoded
}

// NewBatchID returns a sortable, collision-resistant run identifier.
func NewBatchID() string {
	buffer := make([]byte, 6)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("batch-%d", time.Now().UTC().UnixNano())
	}
	return "batch-" + time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(buffer)
}
