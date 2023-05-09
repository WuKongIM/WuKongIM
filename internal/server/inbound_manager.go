package server

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/limlog"
	"github.com/WuKongIM/WuKongIM/pkg/limutil"
	"github.com/WuKongIM/WuKongIM/pkg/lmproto"
	"go.uber.org/zap"
)

type InboundManager struct {
	bitmap *limutil.SlotBitMap

	inboundMap     map[uint32][]uint32
	inboundMapLock sync.RWMutex

	slotNum uint32

	workConds []*sync.Cond

	stopChan chan struct{}
	s        *Server
	limlog.Log
}

func NewInboundManager(s *Server) *InboundManager {
	i := &InboundManager{
		slotNum:  1,
		stopChan: make(chan struct{}),
		s:        s,
		Log:      limlog.NewLIMLog("InboundManager"),
	}
	i.bitmap = limutil.NewSlotBitMap(int(i.slotNum))
	i.inboundMap = make(map[uint32][]uint32, i.slotNum)
	i.workConds = make([]*sync.Cond, i.slotNum)
	for j := 0; j < int(i.slotNum); j++ {
		i.workConds[j] = sync.NewCond(&sync.Mutex{})
	}
	return i
}

func (i *InboundManager) Start() {
	for j := 0; j < int(i.slotNum); j++ {
		go i.workRun(uint32(j))
	}
}

func (i *InboundManager) Stop() {
	close(i.stopChan)
}

// 客户端收件箱开
func (i *InboundManager) AddClientID(clientID uint32) uint32 {
	slot := i.slot(clientID)

	i.workConds[slot].L.Lock()
	defer i.workConds[slot].L.Unlock()

	i.inboundMapLock.RLock()
	clientIDs := i.inboundMap[slot]
	i.inboundMapLock.RUnlock()
	if clientIDs == nil {
		clientIDs = make([]uint32, 0)
	}
	for _, cid := range clientIDs {
		if clientID == cid {
			return slot
		}
	}
	clientIDs = append(clientIDs, clientID)

	i.inboundMapLock.Lock()
	i.inboundMap[slot] = clientIDs
	i.inboundMapLock.Unlock()

	return slot
}

func (i *InboundManager) ClientIDs(slot uint32) []uint32 {
	i.inboundMapLock.RLock()
	defer i.inboundMapLock.RUnlock()
	return i.inboundMap[slot]
}
func (i *InboundManager) ClientIDsNoLock(slot uint32) []uint32 {

	i.inboundMapLock.RLock()
	defer i.inboundMapLock.RUnlock()
	return i.inboundMap[slot]
}

func (i *InboundManager) RemoveClientIDsNoLock(slot uint32, clientIDs []uint32) {
	i.inboundMapLock.RLock()
	oldClientIDs := i.inboundMap[slot]
	i.inboundMapLock.RUnlock()

	newClientIDs := make([]uint32, 0, len(oldClientIDs))
	for _, oldClientID := range oldClientIDs {
		exist := false
		for _, clientID := range clientIDs {
			if oldClientID == clientID {
				exist = true
				break
			}
		}
		if !exist {
			newClientIDs = append(newClientIDs, oldClientID)
		}
	}
	i.inboundMapLock.Lock()
	i.inboundMap[slot] = newClientIDs
	i.inboundMapLock.Unlock()
}

func (i *InboundManager) RemoveClientIDs(slot uint32, clientIDs []uint32) {
	i.workConds[slot].L.Lock()
	defer i.workConds[slot].L.Unlock()
	i.RemoveClientIDsNoLock(slot, clientIDs)
}

func (i *InboundManager) Notify(slot uint32) {
	i.workConds[slot].Signal()
}

func (i *InboundManager) slot(clientID uint32) uint32 {
	data := make([]byte, 4)
	binary.BigEndian.PutUint32(data, clientID)
	return crc32.ChecksumIEEE(data) % i.slotNum
}

func (i *InboundManager) workRun(slot uint32) {
	for {

		i.workConds[slot].L.Lock()

		clientIDs := i.ClientIDsNoLock(slot)
		if len(clientIDs) == 0 {
			i.workConds[slot].Wait()
		}

		clientIDs = i.ClientIDsNoLock(slot)
		if len(clientIDs) > 0 {
			for _, clientID := range clientIDs {
				i.handleInbound(clientID)
			}
			i.RemoveClientIDsNoLock(slot, clientIDs)
		}
		i.workConds[slot].L.Unlock()
	}
}

func (i *InboundManager) handleInbound(clientID uint32) {
	cli := i.s.clientManager.Get(clientID)
	if cli == nil {
		i.Debug("客户端不存在！", zap.Uint32("clientID", clientID))
		return
	}
	err := i.inboundRead(cli)
	if err != nil {
		i.Error("读取收件箱错误！", zap.Error(err), zap.Uint32("clientID", clientID))
	}
}

func (i *InboundManager) inboundRead(cli *client) error {

	cli.in.start = time.Now()

	msgBytes := cli.InboundBytes() // 获取到消息
	if len(msgBytes) == 0 {
		return errors.New("收件箱没有数据！")
	}

	// 处理包
	offset := 0
	frames := make([]lmproto.Frame, 0)
	for len(msgBytes) > offset {
		frame, size, err := i.s.opts.Proto.DecodeFrame(msgBytes[offset:], cli.Version())
		if err != nil { //
			i.Warn("Failed to decode the message", zap.Error(err))
			cli.close()
			return err
		}

		frames = append(frames, frame)
		offset += size
	}
	cli.InboundDiscord(len(msgBytes)) // 删除掉客户端收件箱处理了的字节数量的大小

	i.s.handleGoroutinePool.Submit(func() {
		i.s.handleFrames(frames, cli)
	})

	return nil
}
