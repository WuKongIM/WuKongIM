package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"slices"
)

// ConversationReplicaRecovery authorizes one exact original replica group.
// A zero source is permitted only for archive-only treatment of that group.
type ConversationReplicaRecovery struct {
	LogicalKey   string `json:"logical_key"`
	SourceNodeID uint64 `json:"source_node_id"`
	ArchiveOnly  bool   `json:"archive_only,omitempty"`
	// CopiesSHA256 binds every formal replica's candidate and original row,
	// including absence. See conversationReplicaCopies for the framed digest.
	CopiesSHA256 string `json:"copies_sha256"`
}

// ConversationReplicaSelection counts explicit logical-group decisions.
// Every physical original remains in the source archive in either outcome.
type ConversationReplicaSelection struct {
	Retained uint64 `json:"retained"`
	Archived uint64 `json:"archived"`
}

func validateConversationReplicaPolicy(p *MetadataPolicy) error {
	if p == nil || len(p.ConversationReplicas) == 0 {
		return nil
	}
	if p.ConversationLookup != "v2_active_slot" || len(p.ConversationReplicas) > 1024 {
		return errors.New("conversation replica recovery requires original lookup semantics and at most 1024 exact groups")
	}
	seen := map[string]bool{}
	for _, r := range p.ConversationReplicas {
		b, err := hex.DecodeString(r.CopiesSHA256)
		if r.LogicalKey == "" || len(r.LogicalKey) > 16384 || seen[r.LogicalKey] || r.ArchiveOnly != (r.SourceNodeID == 0) || err != nil || len(b) != 32 || hex.EncodeToString(b) != r.CopiesSHA256 {
			return errors.New("invalid or duplicate exact conversation replica recovery")
		}
		seen[r.LogicalKey] = true
	}
	return nil
}

// conversationReplicaChoices owns one selection pass's bounded decisions and
// consumption counts; no proof is reused across captures or archive rebuilds.
type conversationReplicaChoices struct {
	ctx    context.Context
	w      Workspace
	rules  map[string]ConversationReplicaRecovery
	used   map[string]uint64
	counts ConversationReplicaSelection
}

func newConversationReplicaChoices(ctx context.Context, w Workspace, p *MetadataPolicy) *conversationReplicaChoices {
	c := &conversationReplicaChoices{ctx: ctx, w: w, rules: map[string]ConversationReplicaRecovery{}, used: map[string]uint64{}}
	if p != nil {
		for _, r := range p.ConversationReplicas {
			c.rules[r.LogicalKey] = r
		}
	}
	return c
}

// conversationReplicaCopies hashes ascending-node lines. Present lines are
// "node candidateSHA originalSHA\n"; absent lines are "node absent\n".
// Both hashes cover exact workspace bytes, including original timestamps.
func conversationReplicaCopies(ctx context.Context, w Workspace, row sourceCandidate) (string, map[uint64]sourceCandidate, error) {
	nodes := slices.Clone(row.Group.Replicas)
	slices.Sort(nodes)
	h := sha256.New()
	copies := map[uint64]sourceCandidate{}
	for i, n := range nodes {
		if n == 0 || (i > 0 && n == nodes[i-1]) {
			return "", nil, errors.New("invalid conversation replica group")
		}
		data, found, err := w.Get(ctx, candidateKey("metadata", n, "Conversation", row.LogicalKey))
		if err != nil {
			return "", nil, err
		}
		if !found {
			fmt.Fprintf(h, "%d absent\n", n)
			continue
		}
		var other sourceCandidate
		if err := UnmarshalState(data, &other); err != nil {
			return "", nil, err
		}
		if other.NodeID != n || other.Table != "Conversation" || other.Kind != Primary || other.LogicalKey != row.LogicalKey || !reflect.DeepEqual(other.Group, row.Group) {
			return "", nil, errors.New("conversation replica candidate changes identity or authority")
		}
		raw, found, err := w.Get(ctx, other.SourceKey)
		if err != nil {
			return "", nil, err
		}
		if !found {
			return "", nil, errors.New("conversation replica original is missing")
		}
		fmt.Fprintf(h, "%d %s %s\n", n, diagnosticSHA(data), diagnosticSHA(raw))
		copies[n] = other
	}
	return hex.EncodeToString(h.Sum(nil)), copies, nil
}

// choose checks every original copy once before applying an exact decision.
// It also runs for agreeing followers so a chosen non-Leader cannot be lost.
func (c *conversationReplicaChoices) choose(row sourceCandidate) (uint64, bool, error) {
	if c == nil || row.Table != "Conversation" {
		return 0, false, nil
	}
	rule, ok := c.rules[row.LogicalKey]
	if !ok {
		return 0, false, nil
	}
	if selected, ok := c.used[row.LogicalKey]; ok {
		return selected, true, nil
	}
	digest, copies, err := conversationReplicaCopies(c.ctx, c.w, row)
	if err != nil {
		return 0, true, err
	}
	if digest != rule.CopiesSHA256 {
		return 0, true, errors.New("approved conversation replica evidence changed")
	}
	states := map[string]bool{}
	for _, r := range copies {
		states[r.Digest] = true
	}
	if len(copies) == len(row.Group.Replicas) && len(states) == 1 {
		return 0, true, errors.New("conversation replica recovery targets an agreeing group")
	}
	if !rule.ArchiveOnly {
		if _, ok := copies[rule.SourceNodeID]; !ok {
			return 0, true, errors.New("approved conversation source replica is absent")
		}
		c.counts.Retained++
	} else {
		c.counts.Archived++
	}
	c.used[row.LogicalKey] = rule.SourceNodeID
	return rule.SourceNodeID, true, nil
}

// report rejects stale rules that did not participate in this selection pass.
func (c *conversationReplicaChoices) report() (*ConversationReplicaSelection, error) {
	if len(c.rules) == 0 {
		return nil, nil
	}
	if len(c.used) != len(c.rules) {
		return nil, errors.New("approved conversation replica recovery was not applied")
	}
	r := c.counts
	return &r, nil
}
