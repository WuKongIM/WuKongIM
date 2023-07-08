package server

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
	"github.com/panjf2000/ants/v2"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type DeliveryManager struct {
	s               *Server
	deliveryMsgPool *ants.Pool
	wklog.Log
}

func NewDeliveryManager(s *Server) *DeliveryManager {
	options := ants.Options{ExpiryDuration: 10 * time.Second, Nonblocking: false}
	deliveryMsgPool, err := ants.NewPool(s.opts.DeliveryMsgPoolSize, ants.WithOptions(options), ants.WithPanicHandler(func(err interface{}) {
		fmt.Println("消息投递panic->", errors.Wrap(err.(error), "error"))

	}))
	if err != nil {
		panic(err)
	}
	return &DeliveryManager{
		s:               s,
		deliveryMsgPool: deliveryMsgPool,
		Log:             wklog.NewWKLog("DeliveryManager"),
	}
}

func (d *DeliveryManager) startDeliveryMessages(messages []*Message, large bool, syncOnceMessageSeqMap map[int64]uint32, subscribers []string, fromUID string, fromDeivceFlag wkproto.DeviceFlag, fromDeviceID string) {
	d.deliveryMsgPool.Submit(func() {
		d.deliveryMessages(messages, large, syncOnceMessageSeqMap, subscribers, fromUID, fromDeivceFlag, fromDeviceID)
	})
}

func (d *DeliveryManager) deliveryMessages(messages []*Message, large bool, syncOnceMessageSeqMap map[int64]uint32, subscribers []string, fromUID string, fromDeivceFlag wkproto.DeviceFlag, fromDeviceID string) {
	if len(subscribers) == 0 || len(messages) == 0 {
		return
	}

	offlineSubscribers := make([]string, 0, len(subscribers)) // 离线订阅者
	for _, subscriber := range subscribers {
		recvConns := d.getRecvConns(subscriber, fromUID, fromDeivceFlag, fromDeviceID)
		if len(recvConns) == 0 {
			if subscriber != fromUID { //自己发给自己的消息不触发离线事件
				offlineSubscribers = append(offlineSubscribers, subscriber)
			}
			continue
		} else {
			hasMaster := false
			for _, recvConn := range recvConns {
				if wkproto.DeviceLevel(recvConn.DeviceLevel()) == wkproto.DeviceLevelMaster {
					hasMaster = true
					break
				}
			}
			if !hasMaster { // 主设备没在线也要进行推送
				offlineSubscribers = append(offlineSubscribers, subscriber)
			}
		}
		d.Debug("消息投递", zap.String("subscriber", subscriber), zap.Any("recvConns", len(recvConns)))
		for _, recvConn := range recvConns {
			recvPackets := make([]wkproto.Frame, 0, len(messages))
			for _, m := range messages {
				cloneMsg, err := m.DeepCopy()
				if err != nil {
					d.Error("消息深度拷贝失败！", zap.Error(err))
					continue
				}
				cloneMsg.ToUID = subscriber
				cloneMsg.toClientID = recvConn.ID()
				if len(syncOnceMessageSeqMap) > 0 && m.SyncOnce && !m.NoPersist {
					seq := syncOnceMessageSeqMap[m.MessageID]
					cloneMsg.MessageSeq = seq
				}

				// 这里需要把channelID改成fromUID 比如A给B发消息，B收到的消息channelID应该是A A收到的消息channelID应该是B
				if cloneMsg.ChannelType == wkproto.ChannelTypePerson && cloneMsg.ChannelID == subscriber {
					cloneMsg.ChannelID = cloneMsg.FromUID
				}
				if !cloneMsg.NoPersist { // 需要存储的消息才进行重试
					d.s.retryQueue.startInFlightTimeout(cloneMsg)
				}
				recvPacket := cloneMsg.RecvPacket
				if subscriber == recvPacket.FromUID { // 如果是自己则不显示红点
					recvPacket.RedDot = false
				}
				payloadEnc, err := encryptMessagePayload(recvPacket.Payload, recvConn)
				if err != nil {
					d.Error("加密payload失败！", zap.Error(err))
					continue
				}
				recvPacket.Payload = payloadEnc

				signStr := recvPacket.VerityString()
				msgKey, err := makeMsgKey(signStr, recvConn)
				if err != nil {
					d.Error("生成MsgKey失败！", zap.Error(err))
					continue
				}
				recvPacket.MsgKey = msgKey

				recvPackets = append(recvPackets, cloneMsg.RecvPacket)
			}
			d.s.dispatch.dataOut(recvConn, recvPackets...)

		}
	}

	if len(offlineSubscribers) > 0 {
		d.Debug("Offline subscribers", zap.Strings("offlineSubscribers", offlineSubscribers))
		for _, msg := range messages {
			d.s.webhook.notifyOfflineMsg(msg, large, offlineSubscribers)

		}
	}

}

func (d *DeliveryManager) startRetryDeliveryMsg(msg *Message) {
	d.deliveryMsgPool.Submit(func() {
		d.retryDeliveryMsg(msg)
	})
}
func (d *DeliveryManager) retryDeliveryMsg(msg *Message) {
	d.Debug("重试消息", zap.Any("msg", msg))
	msg.retryCount++
	if msg.toClientID <= 0 {
		d.Error("非重试消息", zap.String("msg", msg.String()))
		return
	}
	if msg.retryCount > d.s.opts.MessageRetry.MaxCount {
		d.Debug("超过最大重试次数！", zap.Int64("messageID", msg.MessageID), zap.Int("messageMaxRetryCount", d.s.opts.MessageRetry.MaxCount))
		return
	}
	recvConn := d.getRecvConn(msg.toClientID)
	if recvConn == nil {
		d.Debug("用户设备没在线，重试消息结束！", zap.String("uid", msg.ToUID), zap.Int32("msgTimestamp", msg.Timestamp), zap.Int64("messageID", msg.MessageID), zap.String("channelID", msg.ChannelID), zap.Uint8("channelType", msg.ChannelType), zap.Int64("toClientID", msg.toClientID))
		return
	}
	channelID := msg.ChannelID
	if msg.ChannelType == wkproto.ChannelTypePerson && msg.ChannelID == msg.ToUID {
		channelID = msg.FromUID
	}
	recvPacket := msg.RecvPacket
	recvPacket.ChannelID = channelID

	d.s.retryQueue.startInFlightTimeout(msg)
	d.s.dispatch.dataOut(recvConn, recvPacket)
}

// get recv
func (d *DeliveryManager) getRecvConns(subscriber string, fromUID string, fromDeivceFlag wkproto.DeviceFlag, fromDeviceID string) []wknet.Conn {
	toConns := d.s.connManager.GetConnsWithUID(subscriber)
	d.Debug("GetConnsWithUID", zap.String("subscriber", subscriber), zap.Int("conns", len(toConns)))
	conns := make([]wknet.Conn, 0, len(toConns))
	if len(toConns) > 0 {
		for _, conn := range toConns {
			if !d.clientIsSelf(conn, fromUID, fromDeivceFlag, fromDeviceID) {
				conns = append(conns, conn)
			}
		}
	}
	return conns
}
func (d *DeliveryManager) getRecvConn(connID int64) wknet.Conn {
	return d.s.connManager.GetConn(connID)
}

// 客户端是发送者自己
func (d *DeliveryManager) clientIsSelf(conn wknet.Conn, fromUID string, fromDeivceFlag wkproto.DeviceFlag, fromDeviceID string) bool {
	if conn.UID() == fromUID && wkproto.DeviceFlag(conn.DeviceFlag()) == fromDeivceFlag && conn.DeviceID() == fromDeviceID {
		return true
	}
	return false
}
