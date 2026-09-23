// Package mqttsession defines entry-neutral MQTT session identities.
package mqttsession

import (
	"errors"
	"strings"
	"unicode/utf8"
)

var ErrInvalidIdentity = errors.New("mqttsession: invalid identity")

// Key is a broker-scoped ClientID, independent of user/device conflict policy.
type Key struct {
	Namespace string `json:"namespace"`
	ClientID  string `json:"client_id"`
}

func (k Key) Validate() error {
	if !ValidIdentity(k.Namespace, 1024) || !ValidIdentity(k.ClientID, 1024) {
		return ErrInvalidIdentity
	}
	return nil
}

// Owner identifies one immutable execution incarnation. ConnectionID is issued
// by the owner registry, never by the client or restored from a previous boot.
type Owner struct {
	Key Key `json:"key"`
	// SessionGeneration is the durable subscription/cursor lifetime.
	SessionGeneration uint64 `json:"session_generation"`
	// OwnerGeneration changes for every committed connection takeover.
	OwnerGeneration uint64 `json:"owner_generation"`
	NodeID          uint64 `json:"node_id"`
	// BootID must change if the owner registry is reconstructed, including restore.
	BootID       string `json:"boot_id"`
	ConnectionID uint64 `json:"connection_id"`
}

func (o Owner) Validate() error {
	if o.Key.Validate() != nil || o.SessionGeneration == 0 || o.OwnerGeneration == 0 || o.NodeID == 0 || !ValidIdentity(o.BootID, 128) || o.ConnectionID == 0 {
		return ErrInvalidIdentity
	}
	return nil
}

// ValidIdentity checks the shared byte/string boundary without logging content.
func ValidIdentity(s string, limit int) bool {
	return len(s) > 0 && len(s) <= limit && utf8.ValidString(s) && !strings.ContainsRune(s, 0) && strings.TrimSpace(s) != ""
}
