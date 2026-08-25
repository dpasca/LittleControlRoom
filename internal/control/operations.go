package control

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

func NewOperationID() (string, error) {
	return newTimestampedID("lcrop")
}

func NewEngineerMessageID() (string, error) {
	return newTimestampedID("lcrmsg")
}

func newTimestampedID(prefix string) (string, error) {
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return fmt.Sprintf("%s_%d_%s", prefix, time.Now().UTC().UnixMilli(), hex.EncodeToString(suffix[:])), nil
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
