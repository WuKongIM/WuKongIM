package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type nodeManager struct {
	sync.RWMutex
	nodes map[uint64]*node
	s     *Server
}

func newNodeManager(s *Server) *nodeManager {
	return &nodeManager{
		nodes: make(map[uint64]*node),
		s:     s,
	}
}

func (n *nodeManager) stop() {
	for _, node := range n.nodes {
		node.stop()
	}
}

func (n *nodeManager) deliver(nodeId uint64, req *deliverReq) {
	n.Lock()
	defer n.Unlock()

	node := n.nodes[nodeId]
	if node == nil {
		node = newNode(nodeId, n.s)
		n.nodes[nodeId] = node
		node.start()
	}
	node.deliver(req)
}

type node struct {
	deliverReq chan []ReactorChannelMessage
	stopper    *syncutil.Stopper
	nodeId     uint64
	s          *Server

	deliverQueue *deliverMsgQueue // 投递队列

	delivering bool

	stepC chan []ReactorChannelMessage

	deliverRespC              chan *deliverResp
	requestDeliverErrCount    int // 请求投递失败连续错误次数
	requestDeliverErrMaxCount int // 请求投递失败最大连续错误次数
	wklog.Log
}

func newNode(nodeId uint64, s *Server) *node {
	return &node{
		deliverReq:                make(chan []ReactorChannelMessage, 1024),
		deliverRespC:              make(chan *deliverResp),
		stepC:                     make(chan []ReactorChannelMessage, 1024),
		stopper:                   syncutil.NewStopper(),
		nodeId:                    nodeId,
		Log:                       wklog.NewWKLog(fmt.Sprintf("deliver.node[%d]", nodeId)),
		deliverQueue:              newDeliverMsgQueue(fmt.Sprintf("deliver.queue.node[%d]", nodeId)),
		s:                         s,
		requestDeliverErrMaxCount: 10,
	}
}

func (n *node) start() {
	n.stopper.RunWorker(n.loop)
	n.stopper.RunWorker(n.loopHandleReq)
}

func (n *node) stop() {
	n.stopper.Stop()
}

func (n *node) loopHandleReq() {
	for {
		select {
		case msgs := <-n.deliverReq:
			n.handleDeliverMsgs(msgs)
		case <-n.stopper.ShouldStop():
			return
		}
	}
}

func (n *node) loop() {
	tk := time.NewTicker(time.Millisecond * 200)
	defer tk.Stop()

	for {
		if n.hasReady() {
			msgs := n.ready()
			if len(msgs) > 0 {
				select {
				case n.deliverReq <- msgs:
				case <-n.stopper.ShouldStop():
					return
				}
			}
		}
		select {
		case <-tk.C:
		case messages := <-n.stepC:
			for _, msg := range messages {
				msg.Index = n.deliverQueue.lastIndex + 1
				n.deliverQueue.appendMessage(msg)
			}
		case resp := <-n.deliverRespC:
			n.delivering = false
			if resp.success || resp.timeout {
				if resp.index > n.deliverQueue.deliveringIndex {
					n.deliverQueue.deliveringIndex = resp.index
					n.deliverQueue.truncateTo(resp.index)
				}
			}
		case <-n.stopper.ShouldStop():
			return
		}
	}
}

func (n *node) deliver(req *deliverReq) {
	select {
	case n.stepC <- req.messages:
	case <-n.stopper.ShouldStop():
		return
	}
}

func (n *node) hasReady() bool {
	if n.delivering {
		return false
	}
	return n.deliverQueue.deliveringIndex < n.deliverQueue.lastIndex
}

func (n *node) ready() []ReactorChannelMessage {
	if n.deliverQueue.deliveringIndex < n.deliverQueue.lastIndex {
		n.delivering = true
		msgs := n.deliverQueue.sliceWithSize(n.deliverQueue.deliveringIndex+1, n.deliverQueue.lastIndex+1, n.s.opts.Deliver.MaxDeliverSizePerNode)
		if len(msgs) == 0 {
			n.Panic("get msgs failed", zap.Uint64("startIndex", n.deliverQueue.deliveringIndex+1), zap.Uint64("endIndex", n.deliverQueue.lastIndex+1), zap.Uint64("maxDeliverSizePerNode", n.s.opts.Deliver.MaxDeliverSizePerNode))
		}
		return msgs
	}
	return nil

}

func (n *node) handleDeliverMsgs(msgs []ReactorChannelMessage) {
	err := n.requestDeliver(msgs)
	var success = true
	var timeout = false
	if err != nil {
		n.requestDeliverErrCount++
		n.Error("request deliver failed", zap.Error(err))
		success = false
	} else {
		n.requestDeliverErrCount = 0
	}

	if n.requestDeliverErrCount > n.requestDeliverErrMaxCount {
		n.Error("request deliver failed too many times", zap.Int("requestDeliverErrCount", n.requestDeliverErrCount))
		timeout = true
	}

	select {
	case n.deliverRespC <- &deliverResp{index: msgs[len(msgs)-1].Index, success: success, timeout: timeout}:
	case <-n.stopper.ShouldStop():
		return
	}
}

func (n *node) requestDeliver(msgs []ReactorChannelMessage) error {

	channelMessages := make([]*ChannelMessages, 0)
	var err error
	for _, msg := range msgs {
		fakeChannelId := msg.SendPacket.ChannelID
		if msg.SendPacket.ChannelType == wkproto.ChannelTypePerson {
			fakeChannelId = GetFakeChannelIDWith(msg.SendPacket.ChannelID, msg.FromUid)
		}
		if msg.SendPacket.Framer.SyncOnce { // 如果是cmd消息，需要转换为cmd消息的channelId
			fakeChannelId = n.s.opts.OrginalConvertCmdChannel(fakeChannelId)
		}
		var exist = false
		for _, channelMessage := range channelMessages {
			if channelMessage.ChannelId == fakeChannelId && channelMessage.ChannelType == msg.SendPacket.ChannelType {
				channelMessage.Messages = append(channelMessage.Messages, msg)
				exist = true
				break
			}
		}
		if !exist {
			ch := n.s.channelReactor.loadOrCreateChannel(fakeChannelId, msg.SendPacket.ChannelType)
			var tg *tag
			if ch.receiverTagKey.Load() != "" {
				tg = n.s.tagManager.getReceiverTag(ch.receiverTagKey.Load())
			} else {
				tg, err = ch.makeReceiverTag()
				if err != nil {
					n.Error("makeReceiverTag failed", zap.Error(err), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", msg.SendPacket.ChannelType))
					return err
				}
			}
			if tg == nil {
				n.Error("tag is nil", zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", msg.SendPacket.ChannelType))
				return fmt.Errorf("tag is nil")
			}
			channelMessages = append(channelMessages, &ChannelMessages{
				ChannelId:   fakeChannelId,
				ChannelType: msg.SendPacket.ChannelType,
				TagKey:      tg.key,
				Messages:    ReactorChannelMessageSet{msg},
			})
		}
	}

	timeoutCtx, cancel := context.WithTimeout(n.s.ctx, time.Second*2)
	defer cancel()

	msgSet := ChannelMessagesSet(channelMessages)
	data, err := msgSet.Marshal()
	if err != nil {
		return err
	}

	resp, err := n.s.cluster.RequestWithContext(timeoutCtx, n.nodeId, "/wk/deliver", data)
	if err != nil {
		return err
	}
	if resp.Status != proto.StatusOK {
		return fmt.Errorf("deliver failed status:%d", resp.Status)
	}
	return nil
}

type ChannelMessages struct {
	ChannelId   string
	ChannelType uint8
	TagKey      string
	Messages    ReactorChannelMessageSet
}

type ChannelMessagesSet []*ChannelMessages

func (c ChannelMessagesSet) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(uint32(len(c)))
	for _, cm := range c {
		enc.WriteString(cm.ChannelId)
		enc.WriteUint8(cm.ChannelType)
		enc.WriteString(cm.TagKey)
		data, err := cm.Messages.Marshal()
		if err != nil {
			return nil, err
		}
		enc.WriteUint32(uint32(len(data)))
		enc.WriteBytes(data)
	}
	return enc.Bytes(), nil
}

func (c *ChannelMessagesSet) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	count, err := dec.Uint32()
	if err != nil {
		return err
	}
	var dataLen uint32
	for i := 0; i < int(count); i++ {
		cm := &ChannelMessages{}
		if cm.ChannelId, err = dec.String(); err != nil {
			return err
		}
		if cm.ChannelType, err = dec.Uint8(); err != nil {
			return err
		}
		if cm.TagKey, err = dec.String(); err != nil {
			return err
		}

		if dataLen, err = dec.Uint32(); err != nil {
			return err
		}
		data, err = dec.Bytes(int(dataLen))
		if err != nil {
			return err
		}
		msgs := ReactorChannelMessageSet{}
		if err := msgs.Unmarshal(data); err != nil {
			return err
		}
		cm.Messages = msgs
		*c = append(*c, cm)
	}
	return nil
}

type deliverResp struct {
	index   uint64
	success bool
	timeout bool // 是否超时
}
