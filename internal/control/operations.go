package control

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

func NewOperationID() (string, error) {
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("generate control operation id: %w", err)
	}
	return fmt.Sprintf("lcrop_%d_%s", time.Now().UTC().UnixMilli(), hex.EncodeToString(suffix[:])), nil
}

func IsExternalOperationID(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), "lcrop_")
}

func (s OperationStatus) Terminal() bool {
	switch s {
	case OperationCompleted, OperationFailed, OperationCanceled:
		return true
	default:
		return false
	}
}
