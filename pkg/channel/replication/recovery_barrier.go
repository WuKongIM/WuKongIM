package replication

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

const recoveryBarrierDomain = "wukongim/channel-recovery-barrier/v1\x00"

type recoveryBarrierResult struct {
	State ReplicaState
}

// writeCurrentTermBarrier appends one deterministic business-neutral entry on
// the recovered prefix. The Channel remains non-writable unless the identical
// barrier is durable locally and on the configured current-voter quorum.
func writeCurrentTermBarrier(ctx context.Context, authority Authority, recovered ReplicaState, dispatcher durabilityDispatcher) (recoveryBarrierResult, error) {
	if ctx == nil || dispatcher == nil || !validAuthority(authority) || !validReplicaState(recovered) ||
		recovered.Committed != recovered.LEO || recovered.LEO == ^uint64(0) {
		return recoveryBarrierResult{}, ch.ErrInvalidConfig
	}
	if recovered.LEO > 0 {
		tail := recovered.TailIdentity
		if authority.ID.ChannelEpoch < tail.ChannelEpoch ||
			(authority.ID.ChannelEpoch == tail.ChannelEpoch && authority.ID.LeaderTerm <= tail.LeaderTerm) {
			return recoveryBarrierResult{}, ch.ErrStaleMeta
		}
	}
	commandID, record := recoveryBarrierContent(authority)
	manifest, entries, ok := ch.SealProposalManifest(ch.ProposalManifest{
		Version:      ch.ProposalManifestVersion,
		ChannelEpoch: authority.ID.ChannelEpoch, LeaderTerm: authority.ID.LeaderTerm, FenceVersion: authority.ID.FenceVersion,
		CommandID: commandID, BaseOffset: recovered.LEO, LastOffset: recovered.LEO + 1,
		PreviousTerm: recovered.TailIdentity.LeaderTerm, PreviousIndex: recovered.LEO,
		PreviousDigest: recovered.TailIdentity.Digest,
	}, []ch.Record{record})
	if !ok || len(entries) != 1 {
		return recoveryBarrierResult{}, ch.ErrInvalidConfig
	}
	proposal := durableProposal{
		first: recovered.LEO + 1, last: recovered.LEO + 1,
		channelKey: authority.Key, channelID: authority.ChannelID, leader: authority.Leader,
		manifest: manifest, records: []ch.Record{record}, committed: recovered.LEO,
	}
	result, err := runDurableRound(ctx, authority.Leader, authority.Voters, authority.WriteQuorum, proposal, dispatcher)
	if err != nil {
		return recoveryBarrierResult{}, err
	}
	if !result.localDurable || result.durableVotes < authority.WriteQuorum || !result.outcome.Durable() {
		return recoveryBarrierResult{}, errDurableQuorumUnavailable
	}
	return recoveryBarrierResult{State: ReplicaState{
		LEO: manifest.LastOffset, Committed: recovered.LEO, Manifest: manifest, TailIdentity: entries[0],
	}}, nil
}

func validAuthority(authority Authority) bool {
	if authority.Key == "" || authority.ChannelID.ID == "" || authority.ID.ChannelEpoch == 0 ||
		authority.ID.LeaderTerm == 0 || authority.ID.FenceVersion == 0 || authority.Leader == 0 {
		return false
	}
	configured, err := validateRecoveryTopology(authority.Voters, authority.WriteQuorum)
	if err != nil {
		return false
	}
	_, leaderIsVoter := configured[authority.Leader]
	return leaderIsVoter
}

func recoveryBarrierContent(authority Authority) (ch.CommandID, ch.Record) {
	hash := sha256.New()
	_, _ = hash.Write([]byte(recoveryBarrierDomain))
	writeBarrierBytes(hash, []byte(authority.Key))
	writeBarrierBytes(hash, []byte(authority.ChannelID.ID))
	_, _ = hash.Write([]byte{authority.ChannelID.Type})
	writeBarrierUint64(hash, authority.ID.ChannelEpoch)
	writeBarrierUint64(hash, authority.ID.LeaderTerm)
	writeBarrierUint64(hash, authority.ID.FenceVersion)
	writeBarrierUint64(hash, uint64(authority.Leader))
	for _, voter := range authority.Voters {
		writeBarrierUint64(hash, uint64(voter))
	}
	writeBarrierUint64(hash, uint64(authority.WriteQuorum))
	digest := hash.Sum(nil)
	commandID := ch.CommandID{}
	copy(commandID[:], digest)
	messageID := binary.BigEndian.Uint64(digest[:8])
	if messageID == 0 {
		messageID = 1
	}
	timestamp := int64(binary.BigEndian.Uint64(digest[8:16]) & math.MaxInt64)
	if timestamp == 0 {
		timestamp = 1
	}
	payload := make([]byte, 1+8*3)
	payload[0] = 1
	binary.BigEndian.PutUint64(payload[1:9], authority.ID.ChannelEpoch)
	binary.BigEndian.PutUint64(payload[9:17], authority.ID.LeaderTerm)
	binary.BigEndian.PutUint64(payload[17:25], authority.ID.FenceVersion)
	return commandID, ch.Record{
		ID: messageID, Epoch: authority.ID.ChannelEpoch, ServerTimestampMS: timestamp,
		SyncOnce: true, Payload: payload, SizeBytes: len(payload),
	}
}

type barrierHashWriter interface {
	Write([]byte) (int, error)
}

func writeBarrierUint64(hash barrierHashWriter, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = hash.Write(encoded[:])
}

func writeBarrierBytes(hash barrierHashWriter, value []byte) {
	writeBarrierUint64(hash, uint64(len(value)))
	_, _ = hash.Write(value)
}
