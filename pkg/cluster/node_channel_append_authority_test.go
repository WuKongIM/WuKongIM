package cluster

import (
	"testing"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestNodeInvalidatesExactChannelAppendAuthorityVersion(t *testing.T) {
	service := &recordingChannelAppendAuthorityInvalidator{noopChannelService: noopChannelService{}}
	node := &Node{channels: service}
	id := channelruntime.ChannelID{ID: "room", Type: 2}

	node.InvalidateChannelAppendAuthority(id, 3, 11, 7, 19)

	if service.calls != 1 || service.id != id || service.leader != 3 || service.epoch != 11 || service.leaderEpoch != 7 || service.routeGeneration != 19 {
		t.Fatalf("invalidation = calls:%d id:%#v leader:%d epoch:%d/%d generation:%d, want exact authority version", service.calls, service.id, service.leader, service.epoch, service.leaderEpoch, service.routeGeneration)
	}
}

type recordingChannelAppendAuthorityInvalidator struct {
	noopChannelService
	calls           int
	id              channelruntime.ChannelID
	leader          channelruntime.NodeID
	epoch           uint64
	leaderEpoch     uint64
	routeGeneration uint64
}

func (s *recordingChannelAppendAuthorityInvalidator) InvalidateAppendAuthority(id channelruntime.ChannelID, leader channelruntime.NodeID, epoch uint64, leaderEpoch uint64, routeGeneration uint64) {
	s.calls++
	s.id = id
	s.leader = leader
	s.epoch = epoch
	s.leaderEpoch = leaderEpoch
	s.routeGeneration = routeGeneration
}
