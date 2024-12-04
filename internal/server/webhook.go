package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/grpcpool"
	"github.com/WuKongIM/WuKongIM/pkg/wkhook"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/panjf2000/ants/v2"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

type webhook struct {
	s *Server
	wklog.Log
	eventPool        *ants.Pool
	httpClient       *http.Client
	webhookGRPCPool  *grpcpool.Pool // webhook grpc客户端
	stoped           chan struct{}
	onlinestatusLock sync.RWMutex
	onlinestatusList []string
	focusEvents      map[string]struct{} // 用户关注的事件类型,如果为空则推送所有类型
}

func newWebhook(s *Server) *webhook {
	eventPool, err := ants.NewPool(s.opts.EventPoolSize, ants.WithPanicHandler(func(err interface{}) {
		s.Error("webhook panic", zap.Any("err", err), zap.Stack("stack"))
	}))
	if err != nil {
		panic(err)
	}
	var (
		webhookGRPCPool *grpcpool.Pool
	)
	if s.opts.WebhookGRPCOn() {
		webhookGRPCPool, err = grpcpool.New(func() (*grpc.ClientConn, error) {
			return grpc.Dial(s.opts.Webhook.GRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithKeepaliveParams(keepalive.ClientParameters{
				Time:    5 * time.Minute, // send pings every 5 minute if there is no activity
				Timeout: 2 * time.Second, // wait 1 second for ping ack before considering the connection dead
			}))
		}, 2, 20, time.Minute*5) // 初始化2个连接 最多20个连接
		if err != nil {
			panic(err)
		}

	}

	// 检查用户配置了关注的事件
	var focusEvents = make(map[string]struct{})
	if len(s.opts.Webhook.FocusEvents) > 0 {
		for _, focusEvent := range s.opts.Webhook.FocusEvents {
			if focusEvent == "" {
				continue
			}
			if _, ok := eventWebHook[focusEvent]; ok {
				focusEvents[focusEvent] = struct{}{}
			}
		}
	}

	return &webhook{
		s:                s,
		Log:              wklog.NewWKLog("Webhook"),
		eventPool:        eventPool,
		webhookGRPCPool:  webhookGRPCPool,
		onlinestatusList: make([]string, 0),
		stoped:           make(chan struct{}),
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 5 * time.Second,
				}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          200,
				MaxIdleConnsPerHost:   200,
				IdleConnTimeout:       300 * time.Second,
				TLSHandshakeTimeout:   time.Second * 5,
				ResponseHeaderTimeout: 5 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		},
		focusEvents: focusEvents,
	}
}

func (w *webhook) Start() {
	go w.notifyQueueLoop()
	go w.loopOnlineStatus()
}

func (w *webhook) Stop() {
	close(w.stoped)
}

// Online 用户设备上线通知
func (w *webhook) Online(uid string, deviceFlag wkproto.DeviceFlag, connId int64, deviceOnlineCount int, totalOnlineCount int) {
	w.onlinestatusLock.Lock()
	defer w.onlinestatusLock.Unlock()
	online := 1
	w.onlinestatusList = append(w.onlinestatusList, fmt.Sprintf("%s-%d-%d-%d-%d-%d", uid, deviceFlag, online, connId, deviceOnlineCount, totalOnlineCount))

	w.Debug("User online", zap.String("uid", uid), zap.String("deviceFlag", deviceFlag.String()), zap.Int64("id", connId))
}

func (w *webhook) Offline(uid string, deviceFlag wkproto.DeviceFlag, connId int64, deviceOnlineCount int, totalOnlineCount int) {
	w.onlinestatusLock.Lock()
	defer w.onlinestatusLock.Unlock()
	online := 0
	// 用户ID-用户设备标记-在线状态-socket ID-当前设备标记下的设备在线数量-当前用户下的所有设备在线数量
	w.onlinestatusList = append(w.onlinestatusList, fmt.Sprintf("%s-%d-%d-%d-%d-%d", uid, deviceFlag, online, connId, deviceOnlineCount, totalOnlineCount))

	w.Debug("User offline", zap.String("uid", uid), zap.String("deviceFlag", deviceFlag.String()))
}

// TriggerEvent 触发事件
func (w *webhook) TriggerEvent(event *Event) {
	if !w.s.opts.WebhookOn() { // 没设置webhook直接忽略
		return
	}
	err := w.eventPool.Submit(func() {
		jsonData, err := json.Marshal(event.Data)
		if err != nil {
			w.Error("webhook的event数据不能json化！", zap.Error(err))
			return
		}

		if w.s.opts.WebhookGRPCOn() {
			err = w.sendWebhookForGRPC(event.Event, jsonData)
		} else {
			err = w.sendWebhookForHttp(event.Event, jsonData)
		}
		if err != nil {
			w.Error("请求webhook失败！", zap.Error(err), zap.String("event", event.Event))
			return
		}

	})
	if err != nil {
		w.Error("提交事件失败", zap.Error(err))
	}
}

func (w *webhook) notifyOfflineMsg(msg ReactorChannelMessage, subscribers []string) {
	compress := ""
	toUIDs := subscribers
	var compresssToUIDs []byte
	if w.s.opts.Channel.SubscriberCompressOfCount > 0 && len(subscribers) > w.s.opts.Channel.SubscriberCompressOfCount {
		buff := new(bytes.Buffer)
		gWriter := gzip.NewWriter(buff)
		defer gWriter.Close()
		_, err := gWriter.Write([]byte(wkutil.ToJSON(subscribers)))
		if err != nil {
			w.Error("压缩订阅者失败！", zap.Error(err))
		} else {
			toUIDs = make([]string, 0)
			compress = "gzip"
			compresssToUIDs = buff.Bytes()
		}
	}
	// 推送离线到上层应用
	w.TriggerEvent(&Event{
		Event: EventMsgOffline,
		Data: MessageOfflineNotify{
			MessageResp: MessageResp{
				Header: MessageHeader{
					RedDot:    wkutil.BoolToInt(msg.SendPacket.RedDot),
					SyncOnce:  wkutil.BoolToInt(msg.SendPacket.SyncOnce),
					NoPersist: wkutil.BoolToInt(msg.SendPacket.NoPersist),
				},
				Setting:      msg.SendPacket.Setting.Uint8(),
				ClientMsgNo:  msg.SendPacket.ClientMsgNo,
				MessageId:    msg.MessageId,
				MessageIdStr: strconv.FormatInt(msg.MessageId, 10),
				MessageSeq:   uint64(msg.MessageSeq),
				FromUID:      msg.FromUid,
				ChannelID:    msg.SendPacket.ChannelID,
				ChannelType:  msg.SendPacket.ChannelType,
				Topic:        msg.SendPacket.Topic,
				Expire:       msg.SendPacket.Expire,
				Timestamp:    int32(time.Now().Unix()),
				Payload:      msg.SendPacket.Payload,
			},
			ToUIDs:          toUIDs,
			Compress:        compress,
			CompresssToUIDs: compresssToUIDs,
			SourceID:        int64(w.s.opts.Cluster.NodeId),
		},
	})
}

// 通知上层应用 TODO: 此初报错可以做一个邮件报警处理类的东西，
func (w *webhook) notifyQueueLoop() {
	errorSleepTime := time.Second * 1 // 发生错误后sleep时间
	ticker := time.NewTicker(w.s.opts.Webhook.MsgNotifyEventPushInterval)
	defer ticker.Stop()
	errMessageIDMap := make(map[int64]int) // 记录错误的消息ID value为错误次数
	if w.s.opts.WebhookOn() {
		for {
			messages, err := w.s.store.GetMessagesOfNotifyQueue(w.s.opts.Webhook.MsgNotifyEventCountPerPush)
			if err != nil {
				w.Error("获取通知队列内的消息失败！", zap.Error(err))
				// 如果系统出现错误，就移除第一个
				err = w.s.store.DB().RemoveMessagesOfNotifyQueueCount(1)
				if err != nil {
					w.Error("RemoveMessagesOfNotifyQueueCount: 移除通知对列消息失败！", zap.Error(err))
				}
				time.Sleep(errorSleepTime) // 如果报错就休息下
				continue
			}
			if len(messages) > 0 {
				messageResps := make([]*MessageResp, 0, len(messages))
				for _, msg := range messages {
					resp := &MessageResp{}
					resp.from(msg, w.s)
					messageResps = append(messageResps, resp)
				}
				messageData, err := json.Marshal(messageResps)
				if err != nil {
					w.Error("第三方消息通知的event数据不能json化！", zap.Error(err))
					time.Sleep(errorSleepTime) // 如果报错就休息下
					continue
				}

				if w.s.opts.WebhookGRPCOn() {
					err = w.sendWebhookForGRPC(EventMsgNotify, messageData)
				} else {
					err = w.sendWebhookForHttp(EventMsgNotify, messageData)
				}
				if err != nil {
					w.Error("请求所有消息通知webhook失败！", zap.Error(err))
					errMessageIDs := make([]int64, 0, len(messages))
					for _, message := range messages {
						errCount := errMessageIDMap[message.MessageID]
						errCount++
						errMessageIDMap[message.MessageID] = errCount
						if errCount >= w.s.opts.Webhook.MsgNotifyEventRetryMaxCount {
							errMessageIDs = append(errMessageIDs, message.MessageID)
						}
					}
					if len(errMessageIDs) > 0 {
						w.Error("消息通知失败超过最大次数！", zap.Int64s("messageIDs", errMessageIDs))
						err = w.s.store.RemoveMessagesOfNotifyQueue(errMessageIDs)
						if err != nil {
							w.Warn("从通知队列里移除消息失败！", zap.Error(err), zap.Int64s("messageIDs", errMessageIDs))
						}
						for _, errMessageID := range errMessageIDs {
							delete(errMessageIDMap, errMessageID)
						}
					}
					time.Sleep(errorSleepTime) // 如果报错就休息下
					continue
				}

				messageIDs := make([]int64, 0, len(messages))
				for _, message := range messages {
					messageID := message.MessageID
					messageIDs = append(messageIDs, messageID)

					delete(errMessageIDMap, messageID)
				}
				err = w.s.store.RemoveMessagesOfNotifyQueue(messageIDs)
				if err != nil {
					w.Warn("从通知队列里移除消息失败！", zap.Error(err), zap.Int64s("messageIDs", messageIDs), zap.String("Webhook", w.s.opts.Webhook.HTTPAddr))
					time.Sleep(errorSleepTime) // 如果报错就休息下
					continue
				}
			}

			select {
			case <-ticker.C:
			case <-w.stoped:
				return
			}
		}
	}
}

func (w *webhook) loopOnlineStatus() {
	if !w.s.opts.WebhookOn() {
		return
	}
	opLen := 0    // 最后一次操作在线状态数组的长度
	errCount := 0 // webhook请求失败重试次数
	for {
		if opLen == 0 {
			w.onlinestatusLock.Lock()
			opLen = len(w.onlinestatusList)
			w.onlinestatusLock.Unlock()
		}
		if opLen == 0 {
			time.Sleep(time.Second * 2) // 没有数据就休息2秒
			continue
		}
		w.onlinestatusLock.Lock()
		data := w.onlinestatusList[:opLen]
		w.onlinestatusLock.Unlock()
		jsonData, err := json.Marshal(data)
		if err != nil {
			w.Error("webhook的event数据不能json化！", zap.Error(err))
			time.Sleep(time.Second * 1)
			continue
		}

		if w.s.opts.WebhookGRPCOn() {
			err = w.sendWebhookForGRPC(EventOnlineStatus, jsonData)
		} else {
			err = w.sendWebhookForHttp(EventOnlineStatus, jsonData)
		}
		if err != nil {
			errCount++
			w.Error("请求在线状态webhook失败！", zap.Error(err))
			if errCount >= w.s.opts.Webhook.MsgNotifyEventRetryMaxCount {
				w.Error("请求在线状态webhook失败通知超过最大次数！", zap.Int("MsgNotifyEventRetryMaxCount", w.s.opts.Webhook.MsgNotifyEventRetryMaxCount))

				w.onlinestatusLock.Lock()
				w.onlinestatusList = w.onlinestatusList[opLen:]
				opLen = 0
				w.onlinestatusLock.Unlock()

				errCount = 0
			}

			time.Sleep(time.Second * 1) // 如果报错就休息下
			continue
		}

		w.onlinestatusLock.Lock()
		w.onlinestatusList = w.onlinestatusList[opLen:]
		opLen = 0
		w.onlinestatusLock.Unlock()

	}
}

func (w *webhook) sendWebhookForHttp(event string, data []byte) error {
	eventURL := fmt.Sprintf("%s?event=%s", w.s.opts.Webhook.HTTPAddr, event)
	startTime := time.Now().UnixNano() / 1000 / 1000
	w.Debug("webhook开始请求", zap.String("eventURL", eventURL))
	if !w.isEventFocused(EventMsgNotify) { // 如果不关注事件就不走后边的推送逻辑
		w.Debug("webhook http非关注事件, 不推送", zap.String("event", event))
		return nil
	}
	resp, err := w.httpClient.Post(eventURL, "application/json", bytes.NewBuffer(data))
	w.Debug("webhook请求结束 耗时", zap.Int64("mill", time.Now().UnixNano()/1000/1000-startTime))
	if err != nil {
		w.Warn("调用第三方消息通知失败！", zap.String("Webhook", w.s.opts.Webhook.HTTPAddr), zap.Error(err))
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		w.Warn("第三方消息通知接口返回状态错误！", zap.Int("status", resp.StatusCode), zap.String("Webhook", w.s.opts.Webhook.HTTPAddr))
		return errors.New("第三方消息通知接口返回状态错误！")
	}
	return nil
}

func (w *webhook) sendWebhookForGRPC(event string, data []byte) error {

	startNow := time.Now()
	startTime := startNow.UnixNano() / 1000 / 1000
	w.Debug("webhook grpc 开始请求", zap.String("event", event))
	if !w.isEventFocused(event) { // 如果不关注事件就不走后边的推送逻辑
		w.Debug("webhook grpc 非关注事件，不推送", zap.String("event", event))
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel()
	clientConn, err := w.webhookGRPCPool.Get(ctx)
	if err != nil {
		return err
	}
	defer clientConn.Close()
	// cliConn, err := grpc.Dial(l.opts.WebhookGRPC, grpc.WithInsecure())
	// if err != nil {
	// 	return err
	// }
	cli := wkhook.NewWebhookServiceClient(clientConn)

	sendCtx, sendCancel := context.WithTimeout(context.Background(), time.Second*10)
	defer sendCancel()
	resp, err := cli.SendWebhook(sendCtx, &wkhook.EventReq{
		Event: event,
		Data:  data,
	})
	w.Debug("webhook grpc 请求结束 耗时", zap.Int64("mill", time.Now().UnixNano()/1000/1000-startTime))

	if err != nil {
		return err
	}
	if resp.Status != wkhook.EventStatus_Success {
		return errors.New("grpc返回状态错误！")
	}
	return nil
}

func (w *webhook) isEventFocused(event string) bool {
	if len(w.focusEvents) == 0 {
		return true
	}
	_, exists := w.focusEvents[event]
	return exists
}

const (
	// EventMsgOffline 离线消息
	EventMsgOffline = "msg.offline"
	// EventMsgNotify 消息通知（将所有消息通知到第三方程序）
	EventMsgNotify = "msg.notify"
	// EventOnlineStatus 用户在线状态
	EventOnlineStatus = "user.onlinestatus"
)

var (
	// eventWebHook 用于快速校验用用户配置的关注事件
	eventWebHook = map[string]map[string]struct{}{
		EventMsgOffline:   {},
		EventMsgNotify:    {},
		EventOnlineStatus: {},
	}
)

// Event Event
type Event struct {
	Event string      `json:"event"` // 事件标示
	Data  interface{} `json:"data"`  // 事件数据
}

func (e *Event) String() string {
	return fmt.Sprintf("Event:%s Data:%v", e.Event, e.Data)
}
