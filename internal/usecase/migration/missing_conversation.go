package migration

import (
	"encoding/hex"
	"errors"
	"fmt"
)

// MissingConversationRecovery authorizes the state of one absent conversation.
// The complete capture binds all original rows; hashes avoid putting UIDs in plans.
// RetainedTail is measured after the approved message exclusions and compaction.
type MissingConversationRecovery struct {
	CaptureDigest string `json:"capture_digest"`
	UIDSHA256     string `json:"uid_sha256"`
	ChannelSHA256 string `json:"channel_sha256"`
	RetainedTail  uint64 `json:"retained_tail"`
	// Visibility is empty for the legacy fully-read recovery, or
	// hidden_until_new_message to preserve list absence and history access.
	Visibility string `json:"visibility,omitempty"`
}

func missingConversationKey(uid string, channel ChannelIdentity) string {
	return diagnosticSHA([]byte(uid)) + "/" + diagnosticSHA([]byte(channelTuple(channel)))
}

func validateMissingConversations(p *MetadataPolicy) error {
	if p == nil {
		return nil
	}
	if len(p.MissingConversations) > 1024 {
		return errors.New("too many missing conversation decisions")
	}
	seen := map[string]bool{}
	for _, r := range p.MissingConversations {
		key := r.UIDSHA256 + "/" + r.ChannelSHA256
		if r.RetainedTail == 0 || seen[key] || (r.Visibility != "" && r.Visibility != "hidden_until_new_message") {
			return errors.New("invalid or duplicate missing conversation decision")
		}
		seen[key] = true
		for _, digest := range []string{r.CaptureDigest, r.UIDSHA256, r.ChannelSHA256} {
			b, err := hex.DecodeString(digest)
			if err != nil || len(b) != 32 || hex.EncodeToString(b) != digest {
				return errors.New("missing conversation recovery requires exact lowercase SHA256 digests")
			}
		}
	}
	return nil
}

// missingConversationDecisions tracks exact use so a stale or unnecessary approval
// cannot silently survive a changed source or an existing conversation.
type missingConversationDecisions struct {
	pins map[string]MissingConversationRecovery
	used map[string]bool
}

func newMissingConversationDecisions(s SourceSelection) (*missingConversationDecisions, error) {
	d := &missingConversationDecisions{pins: map[string]MissingConversationRecovery{}, used: map[string]bool{}}
	if s.Metadata != nil {
		if err := validateMissingConversations(&s.Metadata.Policy); err != nil {
			return nil, err
		}
		for _, r := range s.Metadata.Policy.MissingConversations {
			d.pins[r.UIDSHA256+"/"+r.ChannelSHA256] = r
		}
	}
	return d, nil
}

func (d *missingConversationDecisions) readSeq(m SourceMember, tail uint64) (uint64, error) {
	key := missingConversationKey(m.UID, m.Channel)
	r, ok := d.pins[key]
	if !ok {
		if tail > 0 {
			return 0, fmt.Errorf("subscriber %s has history but no original conversation: visibility compatibility must be resolved", key)
		}
		return 0, nil
	}
	if tail != r.RetainedTail {
		return 0, errors.New("missing conversation retained tail differs from approved decision")
	}
	d.used[key] = true
	if r.Visibility == "hidden_until_new_message" {
		return 0, nil
	}
	return tail, nil
}

func (d *missingConversationDecisions) complete() error {
	if len(d.used) != len(d.pins) {
		return errors.New("unused missing conversation recovery decision")
	}
	return nil
}

// hiddenThrough is independent of read and delete positions; validation and
// consumption are performed by readSeq against the exact retained tail.
func (d *missingConversationDecisions) hiddenThrough(m SourceMember) uint64 {
	r := d.pins[missingConversationKey(m.UID, m.Channel)]
	if r.Visibility == "hidden_until_new_message" {
		return r.RetainedTail
	}
	return 0
}
