package server

import (
	"context"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/network"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type MigrateTask struct {
	s       *Server
	stopper *syncutil.Stopper
	stepFnc func()
	wklog.Log
}

func NewMigrateTask(s *Server) *MigrateTask {
	return &MigrateTask{
		s:       s,
		stopper: syncutil.NewStopper(),
		Log:     wklog.NewWKLog("MigrateTask"),
	}
}

func (m *MigrateTask) Start() error {
	m.stepFnc = m.stepUserImport
	m.stopper.RunWorker(m.loop)

	return nil
}

func (m *MigrateTask) Stop() {
	m.stopper.Stop()
}

func (m *MigrateTask) loop() {

	tk := time.NewTicker(5 * time.Second)

	for {

		m.stepFnc()

		select {
		case <-tk.C:
		case <-m.stopper.ShouldStop():
			return
		}
	}
}

// 用户导入
func (m *MigrateTask) stepUserImport() {
	m.Info("Start importing user data")

	m.Info("Fetch user data from old version")
	users, err := m.getUserFromOldVersion()
	if err != nil {
		m.Error("Fetch user data from old version failed", zap.Error(err))
		return
	}

	m.Info("Import user data to new version")
	for _, user := range users {
		err = m.importUser(user)
		if err != nil {
			m.Error("Import user data failed", zap.Error(err))
			return
		}
	}

	m.Info("User data import completed")

	m.stepFnc = m.stepChannelImport

}

// 频道导入
func (m *MigrateTask) stepChannelImport() {

	m.Info("Start importing channel data")

	m.Info("Fetch channel data from old version")
	channels, err := m.getChannelFromOldVersion()
	if err != nil {
		m.Error("Fetch channel data from old version failed", zap.Error(err))
		return
	}

	m.Info("Import channel data to new version")

	for _, channel := range channels {
		err = m.importChannel(channel)
		if err != nil {
			m.Error("Import channel data failed", zap.Error(err))
			return
		}
	}
	m.Info("Channel data import completed")
	m.stepFnc = m.stepMessageImport
}

// 消息导入
func (m *MigrateTask) stepMessageImport() {
	m.Info("Start importing message data")

	m.Info("Fetch topics from old version")
	var slotNum uint32 = 128
	for i := uint32(0); i < slotNum; i++ {
		topics, err := m.getTopicsFromOldVersion(i)
		if err != nil {
			m.Error("Fetch topics from old version failed", zap.Error(err), zap.Uint32("slot", i))
			return
		}

		m.Info("Import message to new version", zap.Uint32("slot", i))

		for _, topic := range topics {
			err = m.importMessage(topic)
			if err != nil {
				m.Error("Import message failed", zap.Error(err))
				return
			}
		}
	}

	m.Info("Message data import completed")
}

func (m *MigrateTask) getChannelFromOldVersion() ([]*mgChannelResp, error) {

	// get channel data from old version
	resp, err := network.Post(m.getFullUrl("/migrate/allchannels"), nil, nil)
	if err != nil {
		return nil, err
	}

	var channels []*mgChannelResp
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &channels)
	if err != nil {
		return nil, err
	}
	return channels, nil
}

func (m *MigrateTask) getUserFromOldVersion() ([]*mgUserResp, error) {
	// get user data from old version
	resp, err := network.Post(m.getFullUrl("/migrate/allusers"), nil, nil)
	if err != nil {
		return nil, err
	}

	var users []*mgUserResp
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &users)
	if err != nil {
		return nil, err
	}
	return users, nil
}

func (m *MigrateTask) getTopicsFromOldVersion(slotId uint32) ([]string, error) {
	// get topic data from old version
	resp, err := network.Post(m.getFullUrl("/migrate/topics"), []byte(wkutil.ToJSON(map[string]interface{}{
		"slot": slotId,
	})), nil)
	if err != nil {
		return nil, err
	}

	var topics []string
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &topics)
	if err != nil {
		return nil, err
	}
	return topics, nil
}

func (m *MigrateTask) getFullUrl(pth string) string {

	return m.s.opts.OldV1Api + pth
}

func (m *MigrateTask) importUser(user *mgUserResp) error {

	relationData, err := m.getChannelRelationData(user.Uid, wkproto.ChannelTypePerson)
	if err != nil {
		return err
	}

	// add user basic info
	err = m.s.store.AddOrUpdateUserAndDevice(user.Uid, wkproto.DeviceFlag(user.DeviceFlag), wkproto.DeviceLevel(user.DeviceLevel), user.Token)
	if err != nil {
		return err
	}

	// add user allowlist
	if len(relationData.Allowlist) > 0 {
		err = m.s.store.AddAllowlist(user.Uid, wkproto.ChannelTypePerson, relationData.Allowlist)
		if err != nil {
			return err
		}
	}

	// add user denylist
	if len(relationData.Denylist) > 0 {
		err = m.s.store.AddDenylist(user.Uid, wkproto.ChannelTypePerson, relationData.Denylist)
		if err != nil {
			return err
		}
	}

	return nil
}

func (m *MigrateTask) importChannel(channel *mgChannelResp) error {

	relationData, err := m.getChannelRelationData(channel.ChannelID, channel.ChannelType)
	if err != nil {
		return err
	}

	// add channel basic info
	err = m.s.store.AddOrUpdateChannel(wkdb.ChannelInfo{
		ChannelId:   channel.ChannelID,
		ChannelType: channel.ChannelType,
		Ban:         channel.Ban,
		Disband:     channel.Disband,
		Large:       channel.Large,
	})
	if err != nil {
		return err
	}

	// add channel subscribers
	if len(relationData.Subscribers) > 0 {
		err = m.s.store.AddSubscribers(channel.ChannelID, channel.ChannelType, relationData.Subscribers)
		if err != nil {
			return err
		}
	}

	// add channel allowlist
	if len(relationData.Allowlist) > 0 {
		err = m.s.store.AddAllowlist(channel.ChannelID, channel.ChannelType, relationData.Allowlist)
		if err != nil {
			return err
		}
	}

	// add channel denylist
	if len(relationData.Denylist) > 0 {
		err = m.s.store.AddDenylist(channel.ChannelID, channel.ChannelType, relationData.Denylist)
		if err != nil {
			return err
		}
	}

	return nil
}

func (m *MigrateTask) importMessage(topic string) error {

	m.Info("Import message", zap.String("topic", topic))

	if topic == "" || !strings.Contains(topic, "-") {
		m.Info("Invalid topic", zap.String("topic", topic))
		return nil
	}
	topicSplits := strings.Split(topic, "-")
	if len(topicSplits) != 2 {
		m.Info("Invalid topic", zap.String("topic", topic))
		return nil
	}

	channelType := wkutil.ParseUint8(topicSplits[0])
	channelId := topicSplits[1]

	if strings.Contains(channelId, "userqueue_") {
		return nil
	}

	var startMessageSeq uint32 = 0
	var endMessageSeq uint32 = 0
	var limit = 1000

	for {
		resp, err := m.syncMessages(channelId, channelType, startMessageSeq, endMessageSeq, limit)
		if err != nil {
			return err
		}

		if len(resp.Messages) == 0 {
			break
		}
		lastMsg := resp.Messages[len(resp.Messages)-1]
		startMessageSeq = uint32(lastMsg.MessageSeq)

		timeoutCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		messages := make([]wkdb.Message, 0, len(resp.Messages))
		for _, m := range resp.Messages {
			messages = append(messages, newDBMessage(m))
		}

		// append messages
		_, err = m.s.store.AppendMessages(timeoutCtx, channelId, channelType, messages)
		if err != nil {
			return err
		}
	}

	return nil
}

func newDBMessage(m *MessageResp) wkdb.Message {
	return wkdb.Message{
		RecvPacket: wkproto.RecvPacket{
			Framer: wkproto.Framer{
				NoPersist: m.Header.NoPersist == 1,
				RedDot:    m.Header.RedDot == 1,
				SyncOnce:  m.Header.SyncOnce == 1,
			},
			Setting:     wkproto.Setting(m.Setting),
			MessageID:   m.MessageId,
			MessageSeq:  uint32(m.MessageSeq),
			ClientMsgNo: m.ClientMsgNo,
			StreamNo:    m.StreamNo,
			StreamSeq:   m.StreamSeq,
			StreamFlag:  m.StreamFlag,
			Timestamp:   m.Timestamp,
			ChannelID:   m.ChannelID,
			ChannelType: m.ChannelType,
			Topic:       m.Topic,
			FromUID:     m.FromUID,
			Payload:     m.Payload,
		},
	}
}

func (m *MigrateTask) syncMessages(channelId string, channelType uint8, startMessageSeq, endMessageSeq uint32, limit int) (*syncMessageResp, error) {

	loginUid := ""
	realChannelId := channelId
	if channelType == wkproto.ChannelTypePerson {
		from, to := GetFromUIDAndToUIDWith(channelId)
		loginUid = from
		realChannelId = to
	} else {
		realChannelId = channelId
	}

	resp, err := network.Post(m.getFullUrl("/channel/messagesync"), []byte(wkutil.ToJSON(map[string]interface{}{
		"channel_id":        realChannelId,
		"channel_type":      channelType,
		"login_uid":         loginUid,
		"start_message_seq": startMessageSeq,
		"end_message_seq":   endMessageSeq,
		"limit":             limit,
		"pull_mode":         1,
	})), nil)
	if err != nil {
		return nil, err
	}

	var syncResp *syncMessageResp
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &syncResp)
	if err != nil {
		return nil, err
	}

	return syncResp, nil

}

func (m *MigrateTask) getChannelRelationData(channelId string, channelType uint8) (*mgChannelRelationResp, error) {
	// get channel relation data from old version

	resp, err := network.Post(m.getFullUrl("/migrate/channel"), []byte(wkutil.ToJSON(map[string]interface{}{
		"channel_id":   channelId,
		"channel_type": channelType,
	})), nil)
	if err != nil {
		return nil, err
	}

	var relation *mgChannelRelationResp
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &relation)
	if err != nil {
		return nil, err
	}
	return relation, nil
}

type mgUserResp struct {
	Uid         string `json:"uid"`
	Token       string `json:"token"`
	DeviceFlag  uint8  `json:"device_flag"`
	DeviceLevel uint8  `json:"device_level"`
}

type mgChannelResp struct {
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
	Ban         bool   `json:"ban"`     // 是否被封
	Disband     bool   `json:"disband"` // 是否解散
	Large       bool   `json:"large"`   // 是否是超大群
}

type mgChannelRelationResp struct {
	Subscribers []string `json:"subscribers"`
	Allowlist   []string `json:"allowlist"`
	Denylist    []string `json:"denylist"`
}
