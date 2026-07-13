package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// DigestScenario returns the canonical SHA-256 identity of one effective scenario.
func DigestScenario(scenario Scenario) (string, error) {
	encoded, err := json.Marshal(scenario)
	if err != nil {
		return "", fmt.Errorf("marshal effective scenario: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
