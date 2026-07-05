package proxy

import (
	"context"
	"reflect"
	"sort"
	"testing"

	promotedcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
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
		int(userConversationStateRPCServiceID),
		int(channelMigrationRPCServiceID),
		int(cmdConversationStateRPCServiceID),
		int(pluginBindingRPCServiceID),
	}
	sort.Ints(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registered RPC service IDs = %v, want %v", got, want)
	}
}

type promotedRPCRegistrationCluster struct {
	handlers map[uint8]promotedcluster.NodeRPCHandler
}

func (c *promotedRPCRegistrationCluster) RegisterRPC(serviceID uint8, handler promotedcluster.NodeRPCHandler) {
	if c.handlers == nil {
		c.handlers = make(map[uint8]promotedcluster.NodeRPCHandler)
	}
	c.handlers[serviceID] = handler
}

func (c *promotedRPCRegistrationCluster) SlotIDs() []multiraft.SlotID { return nil }

func (c *promotedRPCRegistrationCluster) SlotForKey(string) multiraft.SlotID { return 0 }

func (c *promotedRPCRegistrationCluster) HashSlotForKey(string) uint16 { return 0 }

func (c *promotedRPCRegistrationCluster) HashSlotsOf(multiraft.SlotID) []uint16 { return nil }

func (c *promotedRPCRegistrationCluster) HashSlotTableVersion() uint64 { return 0 }

func (c *promotedRPCRegistrationCluster) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) {
	return 0, errNoLeader
}

func (c *promotedRPCRegistrationCluster) IsLocal(multiraft.NodeID) bool { return false }

func (c *promotedRPCRegistrationCluster) PeersForSlot(multiraft.SlotID) []multiraft.NodeID {
	return nil
}

func (c *promotedRPCRegistrationCluster) RPCService(context.Context, multiraft.NodeID, multiraft.SlotID, uint8, []byte) ([]byte, error) {
	return nil, errNoLeader
}
