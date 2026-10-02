package mqttsession

import (
	"encoding/binary"
	"errors"
)

// ErrWillAttemptCapacity requests bounded cleanup without granting dispatch.
var ErrWillAttemptCapacity = errors.New("mqttwill: dispatch journal capacity exhausted")

// MaxWillAttemptReclamation bounds captured identities in one pressure page.
const MaxWillAttemptReclamation = 16

// WillAttempt identifies one exact dispatch grant, independently of a connection
// Owner. Its canonical encoding carries no body, credential or lease deadline.
type WillAttempt struct {
	Key                               Key
	SessionGeneration, WillGeneration uint64
	NodeID                            uint64
	BootID                            string
	// ExecutionGeneration changes only through the authoritative Will CAS.
	ExecutionGeneration uint64
}

const MaxWillAttemptBytes = 2304

func (a WillAttempt) Validate() error {
	if a.Key.Validate() != nil || a.SessionGeneration == 0 || a.WillGeneration == 0 || a.NodeID == 0 || !ValidIdentity(a.BootID, 128) || a.ExecutionGeneration == 0 {
		return ErrInvalidIdentity
	}
	return nil
}

// MarshalBinary provides a single versioned identity encoding for journal keys
// and node RPC envelopes; those adapters own their separate stages/statuses.
func (a WillAttempt) MarshalBinary() ([]byte, error) {
	if a.Validate() != nil {
		return nil, ErrInvalidIdentity
	}
	b := []byte{1}
	text := func(s string) { b = binary.BigEndian.AppendUint16(b, uint16(len(s))); b = append(b, s...) }
	text(a.Key.Namespace)
	text(a.Key.ClientID)
	b = binary.BigEndian.AppendUint64(b, a.SessionGeneration)
	b = binary.BigEndian.AppendUint64(b, a.WillGeneration)
	b = binary.BigEndian.AppendUint64(b, a.NodeID)
	text(a.BootID)
	b = binary.BigEndian.AppendUint64(b, a.ExecutionGeneration)
	return b, nil
}

// DecodeWillAttempt rejects unsupported, oversized and incomplete identities.
func DecodeWillAttempt(b []byte) (WillAttempt, error) {
	var a WillAttempt
	if len(b) == 0 || len(b) > MaxWillAttemptBytes || b[0] != 1 {
		return a, ErrInvalidIdentity
	}
	offset, valid := 1, true
	text := func(limit int) string {
		if !valid || len(b)-offset < 2 {
			valid = false
			return ""
		}
		n := int(binary.BigEndian.Uint16(b[offset:]))
		offset += 2
		if n > limit || len(b)-offset < n {
			valid = false
			return ""
		}
		s := string(b[offset : offset+n])
		offset += n
		return s
	}
	number := func() uint64 {
		if !valid || len(b)-offset < 8 {
			valid = false
			return 0
		}
		n := binary.BigEndian.Uint64(b[offset:])
		offset += 8
		return n
	}
	a.Key.Namespace, a.Key.ClientID = text(1024), text(1024)
	a.SessionGeneration, a.WillGeneration, a.NodeID = number(), number(), number()
	a.BootID, a.ExecutionGeneration = text(128), number()
	if !valid || offset != len(b) || a.Validate() != nil {
		return WillAttempt{}, ErrInvalidIdentity
	}
	return a, nil
}
