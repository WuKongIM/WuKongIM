package proxy

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

// Failure cases: distinct remote Slots on one node must share an envelope;
// origin replicas must not supply facts, each Slot needs a fresh barrier, and
// a failed barrier must not become an allow or poison another Slot's result.
// Malformed/oversized/duplicate replies must fail closed, not shorten alignment.
func TestSendPermissionNodeBatchAndFreshBarriers(t *testing.T) {
	db, err := metadb.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	remoteCluster := &sendPermissionTestCluster{node: 2}
	remote := New(remoteCluster, db)
	originCluster := &sendPermissionTestCluster{node: 1, remote: remote}
	origin := New(originCluster, db)
	wb := db.NewWriteBatch()
	_, err = wb.ApplySendBan(1, metadb.SendBanMutation{UID: "u", SendBan: 1})
	require.NoError(t, err)
	require.NoError(t, wb.UpsertChannel(2, metadb.Channel{ChannelID: "g", ChannelType: 2, SendBan: 1}))
	require.NoError(t, wb.Commit())
	require.NoError(t, wb.Close())
	reads := []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}, {Kind: PermissionMetadataReadChannel, ChannelID: "g", ChannelType: 2}}
	for i := 1; i <= 2; i++ {
		out := origin.ReadSendPermissionMetadataBatch(context.Background(), reads)
		require.Len(t, out, 2)
		require.NoError(t, out[0].Err)
		require.NoError(t, out[1].Err)
		require.True(t, out[0].Found)
		require.EqualValues(t, 1, out[0].UserPolicy.SendBan)
		require.True(t, out[1].Found)
		require.EqualValues(t, 1, out[1].Channel.SendBan)
		require.EqualValues(t, i, originCluster.calls.Load())
		require.EqualValues(t, i*2, remoteCluster.barriers.Load())
	}
	remoteCluster.failSlot = 1
	out := origin.ReadSendPermissionMetadataBatch(context.Background(), reads)
	require.Error(t, out[0].Err)
	require.NoError(t, out[1].Err)
	require.EqualValues(t, 1, out[1].Channel.SendBan)
	remoteCluster.failSlot = 0
	out = remote.ReadSendPermissionMetadataBatch(context.Background(), reads)
	require.NoError(t, out[0].Err)
	require.EqualValues(t, 0, remoteCluster.calls.Load())
	originCluster.truncate = true
	out = origin.ReadSendPermissionMetadataBatch(context.Background(), reads)
	require.Len(t, out, 2)
	require.Error(t, out[0].Err)
	require.Error(t, out[1].Err)
}

func TestSendPermissionRPCRejectsUnboundedInput(t *testing.T) {
	db, err := metadb.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	s := New(&sendPermissionTestCluster{node: 2}, db)
	for _, raw := range []string{`{}`, `{"format":2,"groups":[]}`, strings.Repeat("x", (1<<20)+1)} {
		_, err := s.handleSendPermissionRPC(context.Background(), []byte(raw))
		require.Error(t, err)
	}
	out := s.ReadSendPermissionMetadataBatch(context.Background(), make([]PermissionMetadataRead, 4097))
	require.Len(t, out, 4097)
	require.Error(t, out[0].Err)
}

type sendPermissionTestCluster struct {
	admitted        atomic.Int64
	checkAdmission  bool
	admissionErr    error
	node            multiraft.NodeID
	remote          *Store
	calls, barriers atomic.Int64
	failSlot        multiraft.SlotID
	truncate        bool
}

func (c *sendPermissionTestCluster) SlotIDs() []multiraft.SlotID { return []multiraft.SlotID{1, 2} }
func (c *sendPermissionTestCluster) SlotForKey(k string) multiraft.SlotID {
	if k == "u" {
		return 1
	}
	return 2
}
func (c *sendPermissionTestCluster) HashSlotForKey(k string) uint16 { return uint16(c.SlotForKey(k)) }
func (c *sendPermissionTestCluster) HashSlotsOf(s multiraft.SlotID) []uint16 {
	return []uint16{uint16(s)}
}
func (c *sendPermissionTestCluster) HashSlotTableVersion() uint64 { return 1 }
func (c *sendPermissionTestCluster) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) {
	return 2, nil
}
func (c *sendPermissionTestCluster) IsLocal(n multiraft.NodeID) bool { return c.node == n }
func (c *sendPermissionTestCluster) PeersForSlot(multiraft.SlotID) []multiraft.NodeID {
	return []multiraft.NodeID{2}
}
func (c *sendPermissionTestCluster) RPCService(ctx context.Context, n multiraft.NodeID, s multiraft.SlotID, id uint8, p []byte) ([]byte, error) {
	c.calls.Add(1)
	if c.truncate {
		return []byte(`{"format":1,"groups":[]}`), nil
	}
	return c.remote.handleSendPermissionRPC(ctx, p)
}
func (c *sendPermissionTestCluster) ReadSlotBarrier(_ context.Context, s multiraft.SlotID) error {
	if c.checkAdmission && c.admitted.Load() == 0 {
		return errors.New("unfenced read")
	}
	c.barriers.Add(1)
	if c.failSlot == s {
		return errors.New("no quorum")
	}
	return nil
}

func (c *sendPermissionTestCluster) SendPermissionRoutes(keys []string) []SendPermissionRoute {
	out := make([]SendPermissionRoute, len(keys))
	for i, k := range keys {
		out[i] = SendPermissionRoute{HashSlot: c.HashSlotForKey(k), Fence: SendPermissionFence{SlotID: uint64(c.SlotForKey(k)), LeaderNodeID: 2, LeaderTerm: 1, ConfigEpoch: 1, RouteRevision: 1}}
	}
	return out
}

func TestSendPermissionReplyRequiresFactTypeAndIdentity(t *testing.T) {
	fence := SendPermissionFence{SlotID: 1, LeaderNodeID: 2}
	q := sendPermissionRequest{Format: 1, Groups: []sendPermissionGroup{{Fence: fence, Reads: []sendPermissionRead{{Index: 0, Read: PermissionMetadataRead{Kind: PermissionMetadataReadChannel, ChannelID: "g", ChannelType: 2}}}}}}
	r := sendPermissionReply{Format: 1, Groups: []sendPermissionGroupReply{{Fence: fence, Status: "ok", Results: []sendPermissionValue{{Index: 0, Kind: PermissionMetadataReadChannel, Fact: metadb.PermissionFact{Found: true, Channel: metadb.Channel{ChannelID: "wrong", ChannelType: 2}}}}}}}
	require.Error(t, validateSendPermissionReply(q, r))
	r.Groups[0].Results[0].Fact.Channel.ChannelID = "g"
	require.NoError(t, validateSendPermissionReply(q, r))
	r.Groups[0].Results[0].Kind = PermissionMetadataReadUserSendPolicy
	require.Error(t, validateSendPermissionReply(q, r))
}

func TestSendPermissionMaintenanceAdmissionIsHeldAndReleased(t *testing.T) {
	db, err := metadb.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	c := &sendPermissionTestCluster{node: 2, checkAdmission: true}
	s := New(c, db)
	reads := []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}}
	out := s.ReadSendPermissionMetadataBatch(context.Background(), reads)
	require.NoError(t, out[0].Err)
	require.Zero(t, c.admitted.Load())
	require.EqualValues(t, 1, c.barriers.Load())
	c.admissionErr = errors.New("maintenance")
	out = s.ReadSendPermissionMetadataBatch(context.Background(), reads)
	require.Error(t, out[0].Err)
	require.EqualValues(t, 1, c.barriers.Load())
	require.Zero(t, c.admitted.Load())
}

func (c *sendPermissionTestCluster) AcquireSendPermissionRead(context.Context) (func(), error) {
	if c.admissionErr != nil {
		return nil, c.admissionErr
	}
	c.admitted.Add(1)
	return func() { c.admitted.Add(-1) }, nil
}

// Malformed envelopes below capacity must release their execution permit.
// Real saturation and rejection before decoding are covered by the bounded-wait
// integration test, which fills the queue with actual concurrent callers.
func TestSendPermissionMalformedEnvelopeReleasesPermit(t *testing.T) {
	db, err := metadb.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	s := New(&sendPermissionTestCluster{node: 2}, db)
	_, err = s.handleSendPermissionRPC(context.Background(), []byte("invalid JSON"))
	require.Error(t, err)
	require.Zero(t, s.permissionInflight.Load())
}
