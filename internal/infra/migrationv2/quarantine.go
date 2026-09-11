package migrationv2

import (
	"encoding/binary"
	"errors"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
)

// InspectQuarantine recognizes only the operator-facing malformed-row reasons.
// It never repairs channel bytes or guesses the business owner of an orphan.
func (r Reader) InspectQuarantine(row Row, reason string) (f migration.QuarantineFacts, err error) {
	if row.Kind != Primary {
		return f, errors.New("quarantine requires an exact primary row")
	}
	switch reason {
	case "invalid_message_channel":
		if row.Table != "Message" {
			break
		}
		_, identityErr := r.Identify(row)
		if identityErr == nil {
			break
		}
		switch identityErr.Error() {
		case "Message has invalid channel identity", "v2 message channel identity does not match its key":
		default:
			return f, errors.New("quarantine message failure is outside the approved identity reason")
		}
		if len(row.Key) != 20 || binary.BigEndian.Uint64(row.Key[4:12]) != row.Owner || binary.BigEndian.Uint64(row.Key[12:]) != row.ID || row.ID == 0 {
			return f, errors.New("invalid quarantine message physical position")
		}
		id, err := scalar64(row, "MessageId")
		if err != nil {
			return f, err
		}
		return migration.QuarantineFacts{Owner: row.Owner, Sequence: row.ID, MessageID: id}, nil
	case "unresolved_allowlist_channel":
		if row.Table != "Allowlist" {
			break
		}
		id, err := r.Identify(row)
		if err != nil {
			return f, err
		}
		if id.Channel.ID != "" || id.ChannelHash == 0 {
			break
		}
		return migration.QuarantineFacts{UnresolvedChannel: id.ChannelHash}, nil
	case "inconsistent_cmd_conversation":
		if row.Table != "Conversation" || len(row.Fields["Type"]) != 1 || row.Fields["Type"][0] != 0 || !strings.HasSuffix(string(row.Fields["ChannelId"]), "____cmd") {
			break
		}
		id, err := r.Identify(row)
		if err != nil {
			return f, err
		}
		_, err = r.DecodeBusiness(row, id)
		if err == nil || err.Error() != "original conversation type differs from command-channel identity" {
			break
		}
		return f, nil
	}
	return f, errors.New("source primary does not match its approved quarantine reason")
}

// QuarantineIndexPrimary resolves only real persisted index pointers. An ID
// index pointing at a later duplicate is never removed with an earlier row.
func (Reader) QuarantineIndexPrimary(row Row) ([]byte, error) {
	if row.Table != "Message" && row.Table != "Conversation" && row.Table != "Allowlist" {
		return nil, nil
	}
	facts, err := describeStoredIndex(row)
	if err != nil {
		return nil, err
	}
	if facts.Actual != nil && len(facts.Actual.PrimaryKey) > 0 {
		return facts.Actual.PrimaryKey, nil
	}
	var owner, id uint64
	switch row.Table {
	case "Allowlist":
		if row.Kind == Index {
			owner = binary.BigEndian.Uint64(row.Key[6:14])
			id = binary.BigEndian.Uint64(row.Value)
			if id != binary.BigEndian.Uint64(row.Key[14:22]) {
				return nil, errors.New("allowlist index value differs from its UID hash")
			}
		} else {
			owner = binary.BigEndian.Uint64(row.Key[6:14])
			id = binary.BigEndian.Uint64(row.Key[22:30])
		}
	case "Conversation":
		if row.Kind != SecondaryIndex {
			return nil, nil
		}
		owner = binary.BigEndian.Uint64(row.Key[4:12])
		id = binary.BigEndian.Uint64(row.Key[22:30])
	case "Message":
		// Shape-checked administrative timestamp indexes end in owner and sequence.
		if row.Kind != Index || len(row.Key) != 30 {
			return nil, nil
		}
		owner = binary.BigEndian.Uint64(row.Key[14:22])
		id = binary.BigEndian.Uint64(row.Key[22:30])
	}
	key := append([]byte(nil), row.Key[:4]...)
	key[2] = byte(Primary)
	key = append(key, uint64Bytes(owner)...)
	key = append(key, uint64Bytes(id)...)
	return key, nil
}
