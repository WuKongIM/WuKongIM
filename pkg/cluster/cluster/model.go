package cluster

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	ErrStopped            = errors.New("cluster stopped")
	ErrStepChannelFull    = errors.New("step channel full")
	ErrProposeChannelFull = errors.New("propose channel full")
	ErrRecvChannelFull    = errors.New("recv channel full")
)

const (
	MsgUnknown = iota
	MsgReplicaMsg
)

func ChannelKey(channelID string, channelType uint8) string {
	return fmt.Sprintf("%d-%s", channelType, channelID)
}

func ChannelFromChannelKey(channelKey string) (channelID string, channelType uint8) {
	channels := strings.Split(channelKey, "-")
	if len(channels) == 2 {
		channelTypeI, _ := strconv.Atoi(channels[0])
		return channels[1], uint8(channelTypeI)
	}
	return "", 0
}

// 频道分布式配置
type ChannelClusterConfig struct {
	ChannelID   string   // 频道ID
	ChannelType uint8    // 频道类型
	Replicas    []uint64 // 集群节点ID
	LeaderId    uint64   // 领导者ID
	Term        uint32   // 任期
}
