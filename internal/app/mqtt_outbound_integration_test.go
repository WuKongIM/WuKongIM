//go:build integration

package app

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	access "github.com/WuKongIM/WuKongIM/internal/access/mqtt"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	gt "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/eclipse/paho.golang/paho"
	"github.com/stretchr/testify/require"
)

type mqttOutboundCapture struct {
	*access.Handler
	opened chan gt.Context
}

func (h *mqttOutboundCapture) OnSessionOpen(g gt.Context) error {
	if err := h.Handler.OnSessionOpen(g); err != nil {
		return err
	}
	h.opened <- g
	return nil
}
func mqttGatewayOwner(s *meta.MQTTSession) contract.Owner {
	return contract.Owner{Key: contract.Key{Namespace: s.Namespace, ClientID: s.ClientID}, SessionGeneration: s.Generation, OwnerGeneration: s.OwnerGeneration, NodeID: s.OwnerNodeID, BootID: s.OwnerBootID, ConnectionID: s.ConnectionID}
}
func awaitMQTTGatewayPublication(t *testing.T, ctx context.Context, received <-chan *paho.Publish) *paho.Publish {
	t.Helper()
	select {
	case p := <-received:
		return p
	case <-ctx.Done():
		t.Fatal("outbound publication not received")
		return nil
	}
}

// This seam intentionally controls subscription/accounting/admission and its
// content-reference assertion. Socket delivery, takeover and ACK Slot commits are
// real; source-proof and product delivery scheduling acceptance remain separate.
func prepareMQTTGatewayOutbound(t *testing.T, ctx context.Context, node *cluster.Node, s *meta.MQTTSession, m ch.Message, topic string) access.OutboundDelivery {
	t.Helper()
	o := mqttGatewayOwner(s)
	revision := s.Revision
	sub := meta.MQTTSubscription{Namespace: s.Namespace, ClientID: s.ClientID, SessionGeneration: s.Generation, Topic: topic, Generation: revision + 1, TargetKind: meta.MQTTSubscriptionGroup, TargetID: m.ChannelID, GrantedQoS: 1, Stage: meta.MQTTSubscriptionPreparing, OperationID: "gateway-outbound", RecoveryAtMS: time.Now().UnixMilli()}
	for _, stage := range []meta.MQTTSubscriptionStage{meta.MQTTSubscriptionPreparing, meta.MQTTSubscriptionActive} {
		sub.Stage = stage
		sub.Revision = revision + 1
		sub.UpdatedAtMS = time.Now().UnixMilli()
		if stage == meta.MQTTSubscriptionActive {
			sub.RecoveryAtMS = 0
		}
		r, e := node.MutateMQTTSubscription(ctx, meta.MQTTSubscriptionMutation{ExpectedRevision: revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Subscription: sub})
		require.NoError(t, e)
		require.Equal(t, meta.MQTTSessionCASApplied, r.Status)
		revision = r.CurrentRevision
	}
	key := meta.MQTTDeliveryCursorKey{Namespace: s.Namespace, ClientID: s.ClientID, SessionGeneration: s.Generation, SubscriptionGeneration: sub.Generation, SourceKind: meta.MQTTSourceChannel, SourceID: "2:" + m.ChannelID, SourceGeneration: "controlled-gateway-source"}
	size := uint64(len(m.Payload) + len(m.PublicationMetadata))
	for _, op := range []meta.MQTTDeliveryCursorOp{meta.MQTTCursorInit, meta.MQTTCursorAccount} {
		mutation := meta.MQTTDeliveryCursorMutation{Key: key, ExpectedRevision: revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Op: op, Topic: topic, Through: m.MessageSeq - 1, UpdatedAtMS: time.Now().UnixMilli()}
		if op == meta.MQTTCursorAccount {
			mutation.Through = m.MessageSeq
			mutation.AddedMessages = 1
			mutation.AddedBytes = size
		}
		r, e := node.MutateMQTTDeliveryCursor(ctx, mutation)
		require.NoError(t, e)
		require.Equal(t, meta.MQTTSessionCASApplied, r.Status)
		revision = r.CurrentRevision
	}
	var hash [32]byte
	hash[0] = 1
	r, e := node.MutateMQTTWindow(ctx, meta.MQTTWindowMutation{Key: key, ExpectedRevision: revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Op: meta.MQTTWindowAdmit, Publication: meta.MQTTInflightPublication{Position: m.MessageSeq, MessageID: m.MessageID, MessageSeq: m.MessageSeq, ContentVersion: 1, ContentHash: hex.EncodeToString(hash[:]), Bytes: size}, UpdatedAtMS: time.Now().UnixMilli()})
	require.NoError(t, e)
	require.Equal(t, meta.MQTTWindowApplied, r.Status)
	stored, e := node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadInflight, Namespace: s.Namespace, ClientID: s.ClientID, SessionGeneration: s.Generation, PacketID: r.PacketID})
	require.NoError(t, e)
	require.Len(t, stored.Inflight, 1)
	return access.OutboundDelivery{Owner: o, Exchange: stored.Inflight[0], Publication: ch.MQTTReplayPublication{Message: m, ContentVersion: 1, ContentHash: hash, AccountedBytes: size}}
}
