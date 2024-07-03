package clusterevent

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
)

type EventType int

const (
	EventTypeNone EventType = iota
	// EventTypeConfigInit 初始化配置
	EventTypeConfigInit
)

func (e EventType) String() string {

	switch e {
	case EventTypeNone:
		return "EventTypeNone"
	case EventTypeConfigInit:
		return "EventTypeConfigInit"
	}

	return fmt.Sprintf("EventTypeUnknown[%d]", e)
}

type Message struct {
	Type         EventType         // 事件类型
	Nodes        []*pb.Node        // 节点
	Slots        []*pb.Slot        // 槽
	SlotMigrates []*pb.SlotMigrate // 槽迁移
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
