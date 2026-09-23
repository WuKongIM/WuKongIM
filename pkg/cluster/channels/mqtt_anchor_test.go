package channels

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

type mqttAnchorRuntime struct {
	mqttSourceRuntime
	commit func(context.Context, ch.MQTTReplayAnchorRequest) (ch.MQTTReplayAnchorProof, error)
}

func (r *mqttAnchorRuntime) CommitMQTTReplayAnchor(ctx context.Context, q ch.MQTTReplayAnchorRequest) (ch.MQTTReplayAnchorProof, error) {
	return r.commit(ctx, q)
}

func mqttRoutedAnchorFixture(t *testing.T) (*Service, *mqttFreshMeta, *mqttAnchorRuntime, ch.MQTTReplayAnchorRequest, ch.MQTTReplayAnchorProof) {
	t.Helper()
	_, m, _, source := mqttRoutedSourceFixture(t)
	gen := quorumlog.MQTTSourceGeneration(ch.CommandID{1})
	copy := ch.MQTTReplayCopyReceipt{
		Request: ch.MQTTReplayRequest{ChannelID: source.ChannelID, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedRouteGeneration: 4,
			Range: ch.MQTTReplayRange{Generation: gen, From: 1, Through: 2, Limit: 2, MaxBytes: 200}},
		Leader: 2, Authority: ch.MQTTReplayCopyAuthority(m.meta), WriteQuorum: 2, Copies: []ch.NodeID{1, 2},
		Before: ch.MQTTReplayPrefix{Generation: gen}, After: ch.MQTTReplayPrefix{Generation: gen, Through: 2, TotalBytes: 100, TotalStoredBytes: 200, Digest: [32]byte{2}},
	}
	q := ch.MQTTReplayAnchorRequest{Meta: cloneMeta(m.meta), Copy: copy, MessageID: 90, ServerTimestampMS: 1000}
	anchor, err := q.Anchor()
	require.NoError(t, err)
	p := ch.MQTTReplayAnchorProof{Anchor: anchor, Manifest: ch.ProposalManifest{Version: 5, ChannelEpoch: 2, LeaderTerm: 3, FenceVersion: 4,
		CommandID: ch.CommandID{3}, BaseOffset: 2, LastOffset: 3, PreviousTerm: 3, PreviousIndex: 2, PreviousDigest: ch.EntryDigest{4}, Digest: ch.EntryDigest{5}}}
	r := &mqttAnchorRuntime{commit: func(ctx context.Context, actual ch.MQTTReplayAnchorRequest) (ch.MQTTReplayAnchorProof, error) {
		require.Equal(t, m.meta, actual.Meta, "only fresh Slot metadata reaches the reactor")
		require.Equal(t, q.Copy, actual.Copy)
		require.Equal(t, q.MessageID, actual.MessageID)
		require.Equal(t, q.ServerTimestampMS, actual.ServerTimestampMS)
		_, bounded := ctx.Deadline()
		require.True(t, bounded)
		return p, nil
	}}
	s, err := NewService(Config{LocalNode: 2, MetaSource: m, Runtime: r})
	require.NoError(t, err)
	return s, m, r, q, p
}

func TestMQTTAnchorRouteFreshAuthorityAndProof(t *testing.T) {
	for _, mode := range []string{"success", "route_before", "route_after", "leader_before", "leader_after", "isr_before", "isr_after", "replicas_after", "quorum_after", "status_after", "fence_before", "fence_after", "read_error", "cancel_before", "cancel_after", "runtime_error", "bad_digest", "future_manifest", "duplicate_copy", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			s, m, r, q, want := mqttRoutedAnchorFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			called := false
			original := r.commit
			r.commit = func(c context.Context, actual ch.MQTTReplayAnchorRequest) (ch.MQTTReplayAnchorProof, error) {
				called = true
				p, err := original(c, actual)
				switch mode {
				case "runtime_error":
					return ch.MQTTReplayAnchorProof{}, ch.ErrLogConflict
				case "bad_digest":
					p.Anchor.Digest[0]++
				case "future_manifest":
					p.Manifest.FenceVersion++
				}
				return p, err
			}
			m.after = func(n int) {
				switch mode {
				case "route_before", "route_after":
					if n == 2 || mode == "route_before" {
						m.meta.RouteGeneration++
					}
				case "leader_before", "leader_after":
					if n == 2 || mode == "leader_before" {
						m.meta.Leader = 3
					}
				case "isr_before", "isr_after":
					if n == 2 || mode == "isr_before" {
						m.meta.ISR = []ch.NodeID{2, 3}
					}
				case "replicas_after":
					if n == 2 {
						m.meta.Replicas = append(m.meta.Replicas, 4)
					}
				case "quorum_after":
					if n == 2 {
						m.meta.MinISR = 3
					}
				case "status_after":
					if n == 2 {
						m.meta.Status = ch.StatusCreating
					}
				case "fence_before", "fence_after":
					if n == 2 || mode == "fence_before" {
						m.meta.WriteFence = ch.WriteFence{Token: "moving", Version: 1}
					}
				case "cancel_before":
					if n == 1 {
						cancel()
					}
				case "cancel_after":
					if n == 2 {
						cancel()
					}
				}
			}
			if mode == "read_error" {
				m.fail = context.DeadlineExceeded
			}
			if mode == "duplicate_copy" {
				q.Copy.Copies = []ch.NodeID{2, 2}
			}
			if mode == "unsupported" {
				s.runtime = &fakeRuntime{}
			}
			// Lease/retention are not immutable-copy authority and must come from Slot.
			q.Meta.LeaseUntil = time.Unix(300, 0)
			q.Meta.RetentionThroughSeq = 99
			got, err := s.CommitMQTTReplayAnchor(ctx, q)
			if mode == "success" {
				require.NoError(t, err)
				require.Equal(t, want, got)
				require.Equal(t, 2, m.calls)
			} else {
				require.Error(t, err)
				require.Zero(t, got)
				if strings.HasSuffix(mode, "_before") || mode == "read_error" || mode == "duplicate_copy" || mode == "unsupported" {
					require.False(t, called)
				}
			}
		})
	}
}

func TestMQTTAnchorRouteForwardingAndGatewayReplacement(t *testing.T) {
	s, m, _, q, want := mqttRoutedAnchorFixture(t)
	network := clusternet.NewLocalNetwork()
	gateway := NewServiceGateway(s)
	RegisterServiceHandlersOn(localNetworkRegistrar{network: network, nodeID: 2}, gateway)
	origin, err := NewService(Config{LocalNode: 1, MetaSource: m, Runtime: &fakeRuntime{}, Forward: NewTransportClient(network)})
	require.NoError(t, err)
	got, err := origin.CommitMQTTReplayAnchor(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, want, got)
	gateway.Clear()
	_, err = origin.CommitMQTTReplayAnchor(context.Background(), q)
	require.ErrorIs(t, err, ch.ErrNotReady)
	gateway.Replace(s)
	_, err = origin.CommitMQTTReplayAnchor(context.Background(), q)
	require.NoError(t, err)
	other, err := NewService(Config{LocalNode: 3, MetaSource: m, Runtime: &fakeRuntime{}})
	require.NoError(t, err)
	gateway.Replace(other)
	_, err = origin.CommitMQTTReplayAnchor(context.Background(), q)
	require.ErrorIs(t, err, ch.ErrNotLeader)
	noFresh, err := NewService(Config{LocalNode: 2, MetaSource: NewStaticMetaSource([]ch.Meta{m.meta}), Runtime: s.runtime})
	require.NoError(t, err)
	_, err = noFresh.CommitMQTTReplayAnchor(context.Background(), q)
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
}

func TestMQTTAnchorRPCClosedBoundsEchoAndProof(t *testing.T) {
	_, _, _, q, proof := mqttRoutedAnchorFixture(t)
	req := mqttAnchorForwardRequest{Copy: q.Copy, MessageID: q.MessageID, ServerTimestampMS: q.ServerTimestampMS}
	body, err := encodeMQTTAnchorRequest(req)
	require.NoError(t, err)
	got, err := decodeMQTTAnchorRequest(body)
	require.NoError(t, err)
	require.Equal(t, req, got)
	clear(body)
	require.Equal(t, req, got)
	body, err = encodeMQTTAnchorRequest(req)
	require.NoError(t, err)
	for cut := 0; cut < len(body); cut++ {
		_, err := decodeMQTTAnchorRequest(body[:cut])
		require.Error(t, err)
	}
	badVersion := bytes.Clone(body)
	badVersion[4]++
	for _, bad := range [][]byte{badVersion, append(bytes.Clone(body), 0), make([]byte, mqttAnchorRPCMaxBytes+1)} {
		_, err := decodeMQTTAnchorRequest(bad)
		require.Error(t, err)
	}
	for _, operationErr := range append(mqttSourceStatuses[:], errors.New("untrusted peer text")) {
		b, err := encodeMQTTAnchorReply(req, proof, operationErr)
		require.NoError(t, err)
		actual, err := decodeMQTTAnchorReply(b, req)
		if operationErr == nil {
			require.NoError(t, err)
			require.Equal(t, proof, actual)
		} else {
			require.Error(t, err)
			require.Zero(t, actual)
		}
	}
	reply, err := encodeMQTTAnchorReply(req, proof, nil)
	require.NoError(t, err)
	for cut := 0; cut < len(reply); cut++ {
		_, err := decodeMQTTAnchorReply(reply[:cut], req)
		require.Error(t, err)
	}
	for _, mutate := range []func(*mqttAnchorForwardRequest){
		func(r *mqttAnchorForwardRequest) { r.MessageID++ }, func(r *mqttAnchorForwardRequest) { r.ServerTimestampMS++ },
		func(r *mqttAnchorForwardRequest) { r.Copy.Authority[0]++ }, func(r *mqttAnchorForwardRequest) { r.Copy.Copies = []ch.NodeID{2, 3} },
		func(r *mqttAnchorForwardRequest) { r.Copy.Request.ExpectedRouteGeneration++ },
	} {
		other := req
		mutate(&other)
		_, err := decodeMQTTAnchorReply(reply, other)
		require.Error(t, err)
	}
	badStatus := bytes.Clone(reply)
	badStatus[len(mqttAnchorReplyMagic)+2+len(body)] = 255
	errorWithProof := bytes.Clone(reply)
	errorWithProof[len(mqttAnchorReplyMagic)+2+len(body)] = 1
	for _, bad := range [][]byte{badStatus, errorWithProof, append(bytes.Clone(reply), 0), make([]byte, mqttAnchorRPCMaxBytes+1)} {
		_, err := decodeMQTTAnchorReply(bad, req)
		require.Error(t, err)
	}
	for _, mutate := range []func(*ch.MQTTReplayAnchorProof){
		func(p *ch.MQTTReplayAnchorProof) { p.Anchor.SourceCommand[0]++ }, func(p *ch.MQTTReplayAnchorProof) { p.Anchor.Through++ },
		func(p *ch.MQTTReplayAnchorProof) { p.Anchor.Digest[0]++ }, func(p *ch.MQTTReplayAnchorProof) { p.Manifest.Version = 3 },
		func(p *ch.MQTTReplayAnchorProof) { p.Manifest.FenceVersion++ }, func(p *ch.MQTTReplayAnchorProof) { p.Manifest.LastOffset++ },
	} {
		bad := proof
		mutate(&bad)
		_, err := encodeMQTTAnchorReply(req, bad, nil)
		require.Error(t, err)
	}
	// Historical and idle proofs retain their creating authority and content.
	proof.Manifest.LeaderTerm--
	proof.Manifest.FenceVersion--
	_, err = encodeMQTTAnchorReply(req, proof, nil)
	require.NoError(t, err)
	olderEpoch := proof
	olderEpoch.Manifest.ChannelEpoch--
	olderEpoch.Manifest.LeaderTerm = 99
	olderEpoch.Manifest.FenceVersion = 100
	_, err = encodeMQTTAnchorReply(req, olderEpoch, nil)
	require.NoError(t, err, "historical authority ordering follows Channel epoch, then term and route")
	req.Copy.Before = req.Copy.After
	req.Copy.After.Through = 3
	req.Copy.After.TotalStoredBytes += 100
	req.Copy.After.Digest[0]++
	req.Copy.Request.Range.From = 3
	req.Copy.Request.Range.Through = 3
	req.Copy.Request.Range.Limit = 1
	req.Copy.Request.Range.MaxBytes = 100
	b, err := encodeMQTTAnchorReply(req, proof, nil)
	require.NoError(t, err)
	idle, err := decodeMQTTAnchorReply(b, req)
	require.NoError(t, err)
	require.Equal(t, proof, idle)
	// Maximum supported acknowledgement set remains bounded and lossless.
	req.Copy.Copies = make([]ch.NodeID, 256)
	for i := range req.Copy.Copies {
		req.Copy.Copies[i] = ch.NodeID(i + 1)
	}
	b, err = encodeMQTTAnchorRequest(req)
	require.NoError(t, err)
	require.LessOrEqual(t, len(b), mqttAnchorRPCMaxBytes)
	_, err = decodeMQTTAnchorRequest(b)
	require.NoError(t, err)
	req.Copy.Copies = append(req.Copy.Copies, 257)
	_, err = encodeMQTTAnchorRequest(req)
	require.Error(t, err)
}
