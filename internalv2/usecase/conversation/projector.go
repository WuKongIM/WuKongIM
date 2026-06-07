package conversation

import (
	"context"
	"strings"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	conversationChannelTypePerson uint8 = 1
)

// ProjectorOptions contains the storage ports and fanout bounds for conversation projection.
type ProjectorOptions struct {
	// Store persists UID-owned conversation rows.
	Store ConversationBatchStore
	// Members classifies non-person channels for dense or sparse projection.
	Members MemberSource
	// SmallGroupFanoutLimit is the maximum member count eligible for dense fanout.
	SmallGroupFanoutLimit int
}

// Projector turns committed messages into UID-owned conversation state rows.
type Projector struct {
	store                 ConversationBatchStore
	members               MemberSource
	smallGroupFanoutLimit int
}

// NewProjector creates a conversation projector policy coordinator.
func NewProjector(opts ProjectorOptions) *Projector {
	return &Projector{
		store:                 opts.Store,
		members:               opts.Members,
		smallGroupFanoutLimit: opts.SmallGroupFanoutLimit,
	}
}

// HandleCommitted projects one durable message commit into conversation rows.
func (p *Projector) HandleCommitted(ctx context.Context, event messageevents.MessageCommitted) error {
	if p == nil {
		return nil
	}
	var states []metadb.UserConversationState
	var err error
	if event.ChannelType == conversationChannelTypePerson {
		states, err = p.personalStates(event)
	} else {
		states, err = p.groupStates(ctx, event)
	}
	if err != nil || len(states) == 0 {
		return err
	}
	if p.store == nil {
		return ErrStoreRequired
	}
	return p.store.UpsertUserConversationStatesBatch(ctx, states)
}

func (p *Projector) personalStates(event messageevents.MessageCommitted) ([]metadb.UserConversationState, error) {
	left, right, ok := decodeProjectorPersonChannel(event.ChannelID)
	if !ok {
		return nil, nil
	}
	members := []Member{{UID: left}, {UID: right}}
	if event.FromUID == right {
		members[0], members[1] = members[1], members[0]
	}
	return denseStates(event, members), nil
}

func (p *Projector) groupStates(ctx context.Context, event messageevents.MessageCommitted) ([]metadb.UserConversationState, error) {
	if p.members == nil || p.smallGroupFanoutLimit <= 0 {
		return nil, ErrProjectorConfig
	}
	class, err := p.members.ClassifyMembers(ctx, event.ChannelID, int64(event.ChannelType), p.smallGroupFanoutLimit+1)
	if err != nil {
		return nil, err
	}
	if class.IsSmall && len(class.Members) <= p.smallGroupFanoutLimit {
		return denseStates(event, class.Members), nil
	}
	return sparseSenderState(event, class.Members), nil
}

func denseStates(event messageevents.MessageCommitted, members []Member) []metadb.UserConversationState {
	states := make([]metadb.UserConversationState, 0, len(members))
	for _, member := range members {
		if member.UID == "" {
			continue
		}
		states = append(states, conversationState(event, member, false))
	}
	return states
}

func sparseSenderState(event messageevents.MessageCommitted, members []Member) []metadb.UserConversationState {
	if event.FromUID == "" {
		return nil
	}
	return []metadb.UserConversationState{conversationState(event, senderMember(event.FromUID, members), true)}
}

func conversationState(event messageevents.MessageCommitted, member Member, sparse bool) metadb.UserConversationState {
	floor := joinFloor(member.JoinSeq)
	return metadb.UserConversationState{
		UID:          member.UID,
		ChannelID:    event.ChannelID,
		ChannelType:  int64(event.ChannelType),
		ReadSeq:      floor,
		DeletedToSeq: floor,
		ActiveAt:     event.ServerTimestampMS,
		UpdatedAt:    event.ServerTimestampMS,
		SparseActive: sparse,
	}
}

func joinFloor(joinSeq uint64) uint64 {
	if joinSeq == 0 {
		return 0
	}
	return joinSeq - 1
}

func senderMember(uid string, members []Member) Member {
	for _, member := range members {
		if member.UID == uid {
			return member
		}
	}
	return Member{UID: uid}
}

func decodeProjectorPersonChannel(channelID string) (string, string, bool) {
	parts := strings.Split(channelID, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
