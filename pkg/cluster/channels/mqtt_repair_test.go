package channels

import (
	"bytes"
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

type mqttRepairStore struct {
	channelstore.ChannelStore
	proof                    ch.MQTTReplayAnchorProof
	found                    bool
	loadErr                  error
	page                     ch.MQTTReplayPage
	imports, exports, closed int
	importFn                 func(context.Context, uint64, ch.MQTTReplayPage) (ch.MQTTReplayPrefix, error)
}

func (s *mqttRepairStore) LoadMQTTReplayAnchor(context.Context, uint64) (ch.MQTTReplayAnchorProof, bool, error) {
	return s.proof, s.found, s.loadErr
}
func (s *mqttRepairStore) ExportMQTTReplayAnchor(context.Context, uint64, ch.MQTTReplayRange) (ch.MQTTReplayPage, error) {
	s.exports++
	return s.page, nil
}
func (s *mqttRepairStore) ImportMQTTReplayAnchor(c context.Context, p uint64, b ch.MQTTReplayPage) (ch.MQTTReplayPrefix, error) {
	s.imports++
	return s.importFn(c, p, b)
}
func (s *mqttRepairStore) Close() error { s.closed++; return nil }
func (s *mqttRepairStore) StoreCheckpoint(context.Context, ch.Checkpoint) error {
	panic("repair must not advance HW")
}

type mqttRepairForward struct {
	ForwardClient
	calls int
	fetch func(context.Context, ch.MQTTReplayRepairRequest) (ch.MQTTReplayPage, error)
}

func (f *mqttRepairForward) FetchMQTTReplayRepair(c context.Context, q ch.MQTTReplayRepairRequest) (ch.MQTTReplayPage, error) {
	f.calls++
	return f.fetch(c, q)
}
func (f *mqttRepairForward) ForwardMQTTReplayRepair(context.Context, ch.MQTTReplayRepairRequest) (ch.MQTTReplayPrefix, error) {
	panic("local repair recursively forwarded")
}

func mqttRepairFixture(t *testing.T) (*Service, *mqttFreshMeta, *mqttRepairStore, *mqttCopyFactory, *mqttRepairForward, ch.MQTTReplayRepairRequest) {
	t.Helper()
	s, m, _, r, p := mqttRoutedReplayFixture(t)
	r.Range.Through = p.After.Through
	q := ch.MQTTReplayRepairRequest{Target: 2, Donor: 1, AnchorPosition: 2, Request: r}
	proof := ch.MQTTReplayAnchorProof{Anchor: quorumlog.MQTTReplayAnchor{SourceCommand: ch.CommandID{1}, Through: 1, TotalBytes: p.After.TotalBytes, TotalStoredBytes: p.After.TotalStoredBytes, Digest: p.After.Digest}, Manifest: ch.ProposalManifest{Version: 5, ChannelEpoch: 2, LeaderTerm: 3, FenceVersion: 4, CommandID: ch.CommandID{2}, BaseOffset: 1, LastOffset: 2, PreviousIndex: 1, PreviousTerm: 3, PreviousDigest: ch.EntryDigest{3}, Digest: ch.EntryDigest{4}}}
	st := &mqttRepairStore{proof: proof, found: true, page: p, importFn: func(c context.Context, pos uint64, page ch.MQTTReplayPage) (ch.MQTTReplayPrefix, error) {
		require.Equal(t, uint64(2), pos)
		require.Equal(t, p, page)
		_, bounded := c.Deadline()
		require.True(t, bounded)
		return page.After, nil
	}}
	factory := &mqttCopyFactory{handle: st}
	s.store = factory
	f := &mqttRepairForward{fetch: func(c context.Context, got ch.MQTTReplayRepairRequest) (ch.MQTTReplayPage, error) {
		require.Equal(t, q, got)
		return p, nil
	}}
	s.forward = f
	return s, m, st, factory, f, q
}

func TestMQTTRepairReceiverFencesProofsAndCleanup(t *testing.T) {
	for _, mode := range []string{"ok", "migration_fence", "learner", "missing", "pending_error", "foreign_source", "future_proof", "wrong_anchor", "bad_page", "wrong_digest", "route_before", "route_preimport", "members_preimport", "fence_preimport", "route_after", "cancel_before", "cancel_after", "import_error", "panic", "saturated", "not_replica", "bad_quorum"} {
		t.Run(mode, func(t *testing.T) {
			s, m, st, f, forward, q := mqttRepairFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "migration_fence":
				m.meta.WriteFence = ch.WriteFence{Token: "migration", Version: 1}
			case "learner":
				m.meta.ISR = []ch.NodeID{1, 3}
				m.meta.Leader = 3
			case "missing":
				st.found = false
			case "pending_error":
				st.loadErr = ch.ErrNotReady
			case "foreign_source":
				st.proof.Anchor.SourceCommand = ch.CommandID{9}
			case "future_proof":
				st.proof.Manifest.ChannelEpoch++
			case "wrong_anchor":
				st.proof.Manifest.LastOffset++
			case "bad_page":
				forward.fetch = func(context.Context, ch.MQTTReplayRepairRequest) (ch.MQTTReplayPage, error) {
					return ch.MQTTReplayPage{}, nil
				}
			case "wrong_digest":
				forward.fetch = func(context.Context, ch.MQTTReplayRepairRequest) (ch.MQTTReplayPage, error) {
					p := st.page
					p.Records = append([]ch.MQTTReplayRecord(nil), p.Records...)
					p.After.Digest[0]++
					p.Records[0].Digest = p.After.Digest
					return p, nil
				}
			case "route_before":
				m.meta.RouteGeneration++
			case "route_preimport", "members_preimport", "fence_preimport", "route_after":
				m.after = func(n int) {
					at := 2
					if mode == "route_after" {
						at = 3
					}
					if n != at {
						return
					}
					switch mode {
					case "members_preimport":
						m.meta.Replicas = append(m.meta.Replicas, 4)
					case "fence_preimport":
						m.meta.WriteFence = ch.WriteFence{Token: "changed", Version: 1}
					default:
						m.meta.RouteGeneration++
					}
				}
			case "cancel_before":
				cancel()
			case "cancel_after":
				st.importFn = func(context.Context, uint64, ch.MQTTReplayPage) (ch.MQTTReplayPrefix, error) {
					cancel()
					return st.page.After, nil
				}
			case "import_error":
				st.importFn = func(context.Context, uint64, ch.MQTTReplayPage) (ch.MQTTReplayPrefix, error) {
					return ch.MQTTReplayPrefix{}, ch.ErrLogConflict
				}
			case "panic":
				st.importFn = func(context.Context, uint64, ch.MQTTReplayPage) (ch.MQTTReplayPrefix, error) { panic("import") }
			case "saturated":
				for range cap(s.mqttRepairReceivers) {
					s.mqttRepairReceivers <- struct{}{}
				}
			case "not_replica":
				q.Donor = 4
			case "bad_quorum":
				m.meta.MinISR = 1
			}
			if mode == "panic" {
				require.Panics(t, func() { _, _ = s.RepairMQTTReplay(ctx, q) })
				require.Equal(t, f.opens, st.closed)
				require.Empty(t, s.mqttRepairReceivers)
				return
			}
			got, err := s.RepairMQTTReplay(ctx, q)
			if mode == "ok" || mode == "migration_fence" || mode == "learner" {
				require.NoError(t, err)
				require.Equal(t, st.page.After, got)
				require.Equal(t, 1, st.imports)
			} else {
				require.Error(t, err)
				require.Zero(t, got)
			}
			require.Equal(t, f.opens, st.closed)
			if mode == "missing" || mode == "pending_error" || mode == "foreign_source" || mode == "future_proof" || mode == "wrong_anchor" {
				require.Zero(t, forward.calls)
			}
			if mode == "route_preimport" || mode == "members_preimport" || mode == "fence_preimport" || mode == "bad_page" || mode == "wrong_digest" {
				require.Zero(t, st.imports)
			}
			if mode == "route_after" || mode == "cancel_after" {
				require.Equal(t, 1, st.imports, "post-commit error withholds only the receipt")
			}
		})
	}
}

func TestMQTTRepairRPCStableGatewayAndDonorFences(t *testing.T) {
	target, m, targetStore, _, _, q := mqttRepairFixture(t)
	donor, dm, donorStore, _, _, _ := mqttRepairFixture(t)
	donor.localNode = 1
	network := clusternet.NewLocalNetwork()
	targetGate := NewServiceGateway(target)
	donorGate := NewServiceGateway(donor)
	RegisterServiceHandlersOn(localNetworkRegistrar{network: network, nodeID: 2}, targetGate)
	RegisterServiceHandlersOn(localNetworkRegistrar{network: network, nodeID: 1}, donorGate)
	target.forward = NewTransportClient(network)
	origin, err := NewService(Config{LocalNode: 3, MetaSource: m, Runtime: &fakeRuntime{}, Forward: NewTransportClient(network)})
	require.NoError(t, err)
	got, err := origin.RepairMQTTReplay(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, targetStore.page.After, got)
	require.Equal(t, 1, donorStore.exports)
	donorGate.Clear()
	_, err = origin.RepairMQTTReplay(context.Background(), q)
	require.ErrorIs(t, err, ch.ErrNotReady)
	donorGate.Replace(donor)
	_, err = origin.RepairMQTTReplay(context.Background(), q)
	require.NoError(t, err)
	targetGate.Clear()
	_, err = origin.RepairMQTTReplay(context.Background(), q)
	require.ErrorIs(t, err, ch.ErrNotReady)
	targetGate.Replace(target)
	_, err = donorGate.handleMQTTReplayRepair(context.Background(), mqttRepairRPCRequest{Request: q})
	require.ErrorIs(t, err, ch.ErrNotReplica)
	_, err = targetGate.handleMQTTReplayRepair(context.Background(), mqttRepairRPCRequest{Export: true, Request: q})
	require.ErrorIs(t, err, ch.ErrNotReplica)
	for range cap(donor.mqttRepairDonors) {
		donor.mqttRepairDonors <- struct{}{}
	}
	_, err = donor.exportMQTTReplayRepair(context.Background(), q)
	require.ErrorIs(t, err, ch.ErrBackpressured)
	for len(donor.mqttRepairDonors) > 0 {
		<-donor.mqttRepairDonors
	}
	dm.after = func(n int) { dm.meta.WriteFence = ch.WriteFence{Token: "changed", Version: uint64(n)} }
	_, err = donor.exportMQTTReplayRepair(context.Background(), q)
	require.ErrorIs(t, err, ch.ErrStaleMeta)
	require.Equal(t, donorStore.closed, donorStore.exports)
}

func TestMQTTRepairRPCClosedFramingEchoAndOwnership(t *testing.T) {
	_, _, st, _, _, q := mqttRepairFixture(t)
	for _, export := range []bool{false, true} {
		req := mqttRepairRPCRequest{Export: export, Request: q}
		body, err := encodeMQTTRepairRequest(req)
		require.NoError(t, err)
		got, err := decodeMQTTRepairRequest(body)
		require.NoError(t, err)
		require.Equal(t, req, got)
		for cut := 0; cut < len(body); cut++ {
			_, err = decodeMQTTRepairRequest(body[:cut])
			require.Error(t, err)
		}
		badAction := bytes.Clone(body)
		badAction[5] = 255
		badVersion := bytes.Clone(body)
		badVersion[4]++
		for _, b := range [][]byte{badAction, badVersion, append(bytes.Clone(body), 0), make([]byte, mqttRepairRPCMaxRequestBytes+1)} {
			_, err = decodeMQTTRepairRequest(b)
			require.Error(t, err)
		}
		result := mqttRepairRPCResult{Prefix: st.page.After}
		if export {
			result = mqttRepairRPCResult{Page: st.page}
		}
		reply, err := encodeMQTTRepairReply(req, result, nil)
		require.NoError(t, err)
		decoded, err := decodeMQTTRepairReply(reply, req)
		require.NoError(t, err)
		require.Equal(t, result, decoded)
		for cut := 0; cut < len(reply); cut++ {
			_, err = decodeMQTTRepairReply(reply[:cut], req)
			require.Error(t, err)
		}
		for _, mutate := range []func(*mqttRepairRPCRequest){func(r *mqttRepairRPCRequest) { r.Export = !r.Export }, func(r *mqttRepairRPCRequest) { r.Request.Donor = 3 }, func(r *mqttRepairRPCRequest) { r.Request.Target = 3 }, func(r *mqttRepairRPCRequest) { r.Request.AnchorPosition++ }, func(r *mqttRepairRPCRequest) { r.Request.Request.ExpectedRouteGeneration++ }} {
			other := req
			mutate(&other)
			_, err = decodeMQTTRepairReply(reply, other)
			require.Error(t, err)
		}
		badStatus := bytes.Clone(reply)
		badStatus[len(mqttRepairReplyMagic)+2+len(body)] = 255
		errorWithPayload := bytes.Clone(reply)
		errorWithPayload[len(mqttRepairReplyMagic)+2+len(body)] = 1
		for _, b := range [][]byte{badStatus, errorWithPayload, append(bytes.Clone(reply), 0), make([]byte, mqttRepairRPCMaxReplyBytes+1)} {
			_, err = decodeMQTTRepairReply(b, req)
			require.Error(t, err)
		}
		for _, want := range mqttSourceStatuses[1:] {
			b, err := encodeMQTTRepairReply(req, result, want)
			require.NoError(t, err)
			empty, err := decodeMQTTRepairReply(b, req)
			require.ErrorIs(t, err, want)
			require.Zero(t, empty)
		}
		if export {
			clear(decoded.Page.Records[0].Content)
			again, err := decodeMQTTRepairReply(reply, req)
			require.NoError(t, err)
			require.Equal(t, st.page, again.Page)
		}
	}
}
