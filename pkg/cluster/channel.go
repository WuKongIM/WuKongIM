package cluster

import (
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"go.uber.org/zap"
)

type Channel struct {
	r           *replica.RawReplica
	channelID   string
	channelType uint8
	s           *Server
}

func NewChannel(channelID string, channelType uint8, s *Server) *Channel {
	return &Channel{
		channelID:   channelID,
		channelType: channelType,
		s:           s,
		r:           replica.NewRawReplica(s.opts.NodeID, GetChannelKey(channelID, channelType)),
	}
}

func (c *Channel) Propose(data []byte) error {
	err := c.r.ProposeOnlyLocal(data)
	if err != nil {
		return err
	}

	select {
	case c.s.channelManager.sendSyncNotifyC <- c:
	case <-c.s.channelManager.stopper.ShouldStop():
	}
	err = c.CheckAndCommitLogs()
	if err != nil {
		c.s.Panic("CheckAndCommitLogs failed", zap.Error(err))
	}
	return nil
}

func (c *Channel) HandleSyncNotify(req *replica.SyncNotify) {
	c.r.RequestSyncLogs()
}

func (c *Channel) SendNotifySyncToAll() (bool, error) {
	return c.r.SendNotifySyncToAll()
}

func (c *Channel) CheckAndCommitLogs() error {
	return c.r.CheckAndCommitLogs()
}

func (c *Channel) GetChannelKey() string {
	return GetChannelKey(c.channelID, c.channelType)
}

func GetChannelKey(channelID string, channelType uint8) string {
	return fmt.Sprintf("%d-%s", channelType, channelID)
}

func GetChannelFromChannelKey(channelKey string) (string, uint8) {
	var channelID string
	var channelType uint8
	channelStrs := strings.Split(channelKey, "-")
	if len(channelStrs) == 2 {
		channelType = uint8(channelStrs[0][0])
		channelID = channelStrs[1]
	}
	return channelID, channelType
}
