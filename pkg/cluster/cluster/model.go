package cluster

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"
)

var (
	ErrStopped            = errors.New("cluster stopped")
	ErrStepChannelFull    = errors.New("step channel full")
	ErrProposeChannelFull = errors.New("propose channel full")
	ErrRecvChannelFull    = errors.New("recv channel full")
	ErrSlotNotFound       = errors.New("slot not found")
)

const (
	MsgUnknown          = iota
	MsgShardMsg         // 分区消息
	MsgClusterConfigMsg // 集群配置消息
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

var globalRand = &lockedRand{}

type lockedRand struct {
	mu sync.Mutex
}

func (r *lockedRand) Intn(n int) int {
	r.mu.Lock()
	v, _ := rand.Int(rand.Reader, big.NewInt(int64(n)))
	r.mu.Unlock()
	return int(v.Int64())
}
