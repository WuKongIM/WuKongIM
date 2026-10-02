package channel

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func TestMQTTCopyReceiptRequiresExactCurrentVoterEvidence(t *testing.T) {
	for _, mode := range []string{"ok", "epoch", "route", "leader", "status", "fence", "members", "learner_vote", "duplicate", "unsorted", "no_leader", "no_quorum", "zero_member", "duplicate_member", "foreign_voter", "not_majority", "range", "bytes", "digest", "before", "authority"} {
		t.Run(mode, func(t *testing.T) {
			m := Meta{ID: ChannelID{ID: "copy", Type: 1}, Epoch: 1, LeaderEpoch: 2, RouteGeneration: 3, Leader: 1, Replicas: []NodeID{1, 2, 3, 4}, ISR: []NodeID{1, 2, 3}, MinISR: 2, Status: StatusActive}
			gen := quorumlog.MQTTSourceGeneration(CommandID{1})
			r := MQTTReplayCopyReceipt{Request: MQTTReplayRequest{ChannelID: m.ID, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 2, ExpectedRouteGeneration: 3, Range: MQTTReplayRange{Generation: gen, From: 1, Through: 2, Limit: 2, MaxBytes: 100}}, Leader: 1, WriteQuorum: 2, Copies: []NodeID{1, 2}, Before: MQTTReplayPrefix{Generation: gen}, After: MQTTReplayPrefix{Generation: gen, Through: 2, TotalBytes: 40, TotalStoredBytes: 100, Digest: [32]byte{1}}}
			r.Authority = MQTTReplayCopyAuthority(m)
			switch mode {
			case "epoch":
				m.Epoch++
			case "route":
				m.RouteGeneration++
			case "leader":
				m.Leader = 2
			case "status":
				m.Status = StatusDeleted
			case "fence":
				m.WriteFence = WriteFence{Token: "move", Version: 1}
			case "members":
				m.Replicas = []NodeID{1, 2, 3, 5}
			case "learner_vote":
				r.Copies = []NodeID{1, 4}
			case "duplicate":
				r.Copies = []NodeID{1, 1}
			case "unsorted":
				r.Copies = []NodeID{2, 1}
			case "no_leader":
				r.Copies = []NodeID{2, 3}
			case "no_quorum":
				r.Copies = []NodeID{1}
			case "zero_member":
				m.Replicas[3] = 0
				r.Authority = MQTTReplayCopyAuthority(m)
			case "duplicate_member":
				m.Replicas[3] = 1
				r.Authority = MQTTReplayCopyAuthority(m)
			case "foreign_voter":
				m.ISR[2] = 5
				r.Authority = MQTTReplayCopyAuthority(m)
			case "not_majority":
				m.MinISR = 1
				r.WriteQuorum = 1
				r.Authority = MQTTReplayCopyAuthority(m)
			case "range":
				r.Request.Range.From++
			case "bytes":
				r.After.TotalStoredBytes++
			case "digest":
				r.After.Digest = [32]byte{}
			case "before":
				r.Before.Digest = [32]byte{1}
			case "authority":
				r.Authority[0] ^= 1
			}
			require.Equal(t, mode == "ok", r.ValidFor(m))
		})
	}
}
