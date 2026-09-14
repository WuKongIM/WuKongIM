package proxy

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestNewRegistersRPCHandlersOnPromotedCluster(t *testing.T) {
	cluster := &promotedRPCRegistrationCluster{}

	New(cluster, nil)

	got := make([]int, 0, len(cluster.handlers))
	for serviceID := range cluster.handlers {
		got = append(got, int(serviceID))
	}
	sort.Ints(got)
	want := []int{
		int(runtimeMetaRPCServiceID),
		int(identityRPCServiceID),
		int(subscriberRPCServiceID),
		int(channelRPCServiceID),
		int(permissionBatchRPCServiceID),
		int(channelMigrationRPCServiceID),
		int(pluginBindingRPCServiceID),
		int(membershipRPCServiceID),
		int(messageUpdateRPCServiceID),
	}
	sort.Ints(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registered RPC service IDs = %v, want %v", got, want)
	}
}

func TestNewChannelMetadataStoreRegistersAuthoritativeReadHandlers(t *testing.T) {
	cluster := &promotedRPCRegistrationCluster{}

	NewChannelMetadataStore(cluster, nil)

	got := make([]int, 0, len(cluster.handlers))
	for serviceID := range cluster.handlers {
		got = append(got, int(serviceID))
	}
	sort.Ints(got)
	want := []int{
		int(identityRPCServiceID),
		int(runtimeMetaRPCServiceID),
		int(subscriberRPCServiceID),
		int(channelRPCServiceID),
		int(permissionBatchRPCServiceID),
		int(membershipRPCServiceID),
		int(messageUpdateRPCServiceID),
	}
	sort.Ints(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registered channel metadata RPC service IDs = %v, want %v", got, want)
	}
}

// The default runtime only needs device credentials; it must not expose user scans.
func TestDefaultIdentityRPCRejectsNonDeviceOperations(t *testing.T) {
	cluster := &promotedRPCRegistrationCluster{}
	NewChannelMetadataStore(cluster, nil)
	for _, op := range []string{identityRPCGetUser, identityRPCScanUsersPage} {
		t.Run(op, func(t *testing.T) {
			body, err := encodeIdentityRPCRequestBinary(identityRPCRequest{
				Op: op, SlotID: 1, UID: "user", Limit: 1 << 30,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = cluster.handlers[identityRPCServiceID](context.Background(), body)
			if !errors.Is(err, metadb.ErrInvalidArgument) {
				t.Fatalf("non-device operation error = %v, want invalid argument", err)
			}
		})
	}
}

func TestAuthoritativeReadsRequireConfirmedLocalLeaderForSinglePeerSlot(t *testing.T) {
	cluster := &promotedRPCRegistrationCluster{localNodeID: 1}
	store := &Store{cluster: cluster}

	if store.shouldServeSlotLocally(1) {
		t.Fatal("shouldServeSlotLocally() = true without a confirmed leader")
	}
	cluster.leaderID = 1
	if !store.shouldServeSlotLocally(1) {
		t.Fatal("shouldServeSlotLocally() = false for confirmed local leader")
	}
	cluster.leaderID = 2
	if store.shouldServeSlotLocally(1) {
		t.Fatal("shouldServeSlotLocally() = true for a remote leader")
	}
}

type promotedRPCRegistrationCluster struct {
	handlers    map[uint8]func(context.Context, []byte) ([]byte, error)
	leaderID    multiraft.NodeID
	localNodeID multiraft.NodeID
}

func (c *promotedRPCRegistrationCluster) RegisterSlotProxyRPC(serviceID uint8, handler func(context.Context, []byte) ([]byte, error)) {
	if c.handlers == nil {
		c.handlers = make(map[uint8]func(context.Context, []byte) ([]byte, error))
	}
	c.handlers[serviceID] = handler
}

func (c *promotedRPCRegistrationCluster) SlotIDs() []multiraft.SlotID { return nil }

func (c *promotedRPCRegistrationCluster) SlotForKey(string) multiraft.SlotID { return 0 }

func (c *promotedRPCRegistrationCluster) HashSlotForKey(string) uint16 { return 0 }

func (c *promotedRPCRegistrationCluster) HashSlotsOf(multiraft.SlotID) []uint16 { return nil }

func (c *promotedRPCRegistrationCluster) HashSlotTableVersion() uint64 { return 0 }

func (c *promotedRPCRegistrationCluster) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) {
	if c.leaderID == 0 {
		return 0, errNoLeader
	}
	return c.leaderID, nil
}

func (c *promotedRPCRegistrationCluster) IsLocal(nodeID multiraft.NodeID) bool {
	return c.localNodeID != 0 && nodeID == c.localNodeID
}

func (c *promotedRPCRegistrationCluster) PeersForSlot(multiraft.SlotID) []multiraft.NodeID {
	return nil
}

func (c *promotedRPCRegistrationCluster) RPCService(context.Context, multiraft.NodeID, multiraft.SlotID, uint8, []byte) ([]byte, error) {
	return nil, errNoLeader
}
