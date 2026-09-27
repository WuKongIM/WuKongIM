package proxy

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode/utf8"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// MQTTSessionRoutingKey keeps every lifetime and child of one broker/ClientID
// on the same hash Slot. Lengths are big-endian uint16; the v1 domain and digest
// are durable routing identity and must not change when an owner is replaced.
func MQTTSessionRoutingKey(namespace, clientID string) (string, error) {
	if !mqttRoutingIdentity(namespace, 1024) || !mqttRoutingIdentity(clientID, 1024) {
		return "", metadb.ErrInvalidArgument
	}
	data := []byte("mqtt-session-v1:")
	for _, value := range []string{namespace, clientID} {
		data = binary.BigEndian.AppendUint16(data, uint16(len(value)))
		data = append(data, value...)
	}
	digest := sha256.Sum256(data)
	return "mqtt-session-v1:" + hex.EncodeToString(digest[:]), nil
}

// MQTTSourceRoutingKey shares ordinary Channel/UID metadata authority. The full
// Channel type and log generation remain in the binding key, not the Slot key.
func MQTTSourceRoutingKey(owner metadb.MQTTBindingOwner) (string, error) {
	switch owner.Kind {
	case metadb.MQTTBindingUID:
		if mqttRoutingIdentity(owner.ID, 1024) && owner.Generation == "" {
			return owner.ID, nil
		}
	case metadb.MQTTBindingChannel:
		kind, id, found := strings.Cut(owner.ID, ":")
		n, err := strconv.ParseUint(kind, 10, 8)
		if found && err == nil && n != 0 && strconv.FormatUint(n, 10) == kind &&
			mqttRoutingIdentity(owner.ID, 4096) && mqttRoutingIdentity(id, 4096) && mqttRoutingIdentity(owner.Generation, 128) {
			return id, nil
		}
	}
	return "", metadb.ErrInvalidArgument
}

func mqttRoutingIdentity(value string, limit int) bool {
	return len(value) > 0 && len(value) <= limit && utf8.ValidString(value) && !strings.ContainsRune(value, 0) && strings.TrimSpace(value) != ""
}

func mqttReadRoutingKey(q metadb.MQTTRead) (string, error) {
	if ns, client, ok := q.SessionIdentity(); ok {
		return MQTTSessionRoutingKey(ns, client)
	}
	switch q.Kind {
	case metadb.MQTTReadInboxAdmission:
		return q.AdmissionChannel, nil
	case metadb.MQTTReadMembership:
		return q.MembershipKey.ChannelID, nil
	case metadb.MQTTReadSourceBinding:
		return MQTTSourceRoutingKey(q.BindingKey.Owner)
	case metadb.MQTTReadSourceCandidates, metadb.MQTTReadSourceRetention, metadb.MQTTReadInboxDirectory:
		return MQTTSourceRoutingKey(q.Owner)
	default:
		return "", metadb.ErrInvalidArgument
	}
}

func (s *Store) mqttHashSlotOwner(hashSlot uint16) (multiraft.SlotID, error) {
	if s == nil || s.cluster == nil {
		return 0, errSlotNotFound
	}
	var owner multiraft.SlotID
	found := false
	for _, slot := range s.cluster.SlotIDs() {
		for _, hs := range s.cluster.HashSlotsOf(slot) {
			if hs == hashSlot {
				if found {
					return 0, ErrReadStaleRoute
				}
				owner, found = slot, true
			}
		}
	}
	if !found {
		return 0, errSlotNotFound
	}
	return owner, nil
}

func (s *Store) mqttRouteMatches(slot multiraft.SlotID, hashSlot uint16, q metadb.MQTTRead) bool {
	if q.Recovery() {
		owner, err := s.mqttHashSlotOwner(hashSlot)
		return err == nil && owner == slot
	}
	key, err := mqttReadRoutingKey(q)
	if err != nil || s.cluster.SlotForKey(key) != slot || s.cluster.HashSlotForKey(key) != hashSlot {
		return false
	}
	for _, hs := range s.cluster.HashSlotsOf(slot) {
		if hs == hashSlot {
			return true
		}
	}
	return false
}
