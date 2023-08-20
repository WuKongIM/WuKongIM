package server

import (
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/transporter"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type StorageWriter struct {
	s         *Server
	doCommand func(cmd *transporter.CMDReq) (*transporter.CMDResp, error)
	wklog.Log
}

func NewStorageWriter(s *Server, doCommand func(cmd *transporter.CMDReq) (*transporter.CMDResp, error)) wkstore.StoreWriter {
	return &StorageWriter{
		s:         s,
		doCommand: doCommand,
		Log:       wklog.NewWKLog("StorageWriter"),
	}
}

func (s *StorageWriter) UpdateUserToken(uid string, deviceFlag uint8, deviceLevel uint8, token string) error {
	req := transporter.NewCMDReq(CMDUpdateUserToken.Uint32())
	data := EncodeCMDUserToken(uid, deviceFlag, deviceLevel, token)
	req.Param = data
	_, err := s.doCommand(req)
	return err
}

func (s *StorageWriter) UpdateMessageOfUserCursorIfNeed(uid string, messageSeq uint32) error {
	req := transporter.NewCMDReq(CMDUpdateMessageOfUserCursorIfNeed.Uint32())
	data := EncodeCMDUpdateMessageOfUserCursorIfNeed(uid, messageSeq)
	req.Param = data
	_, err := s.doCommand(req)
	return err
}

func (s *StorageWriter) AddOrUpdateChannel(channelInfo *wkstore.ChannelInfo) error {
	req := transporter.NewCMDReq(CMDAddOrUpdateChannel.Uint32())
	data := EncodeAddOrUpdateChannel(channelInfo)
	req.Param = data
	_, err := s.doCommand(req)
	return err
}

// AddSubscribers 添加订阅者
func (s *StorageWriter) AddSubscribers(channelID string, channelType uint8, uids []string) error {
	req := transporter.NewCMDReq(CMDAddSubscribers.Uint32())
	data := EncodeCMDAddSubscribers(channelID, channelType, uids)
	req.Param = data
	_, err := s.doCommand(req)
	return err
}

// RemoveSubscribers 移除指定频道内指定uid的订阅者
func (s *StorageWriter) RemoveSubscribers(channelID string, channelType uint8, uids []string) error {
	req := transporter.NewCMDReq(CMDRemoveSubscribers.Uint32())
	data := EncodeCMDRemoveSubscribers(channelID, channelType, uids)
	req.Param = data
	_, err := s.doCommand(req)
	return err
}
func (s *StorageWriter) RemoveAllSubscriber(channelID string, channelType uint8) error {
	req := transporter.NewCMDReq(CMDRemoveAllSubscriber.Uint32())
	data := EncodeCMDRemoveAllSubscriber(channelID, channelType)
	req.Param = data
	_, err := s.doCommand(req)
	return err
}

// DeleteChannel 删除频道
func (s *StorageWriter) DeleteChannel(channelID string, channelType uint8) error {
	req := transporter.NewCMDReq(CMDDeleteChannel.Uint32())
	data := EncodeCMDDeleteChannel(channelID, channelType)
	req.Param = data
	_, err := s.doCommand(req)
	return err
}

// AddDenylist 添加频道黑名单
func (s *StorageWriter) AddDenylist(channelID string, channelType uint8, uids []string) error {
	req := transporter.NewCMDReq(CMDAddDenylist.Uint32())
	data := EncodeCMDAddDenylist(channelID, channelType, uids)
	req.Param = data
	_, err := s.doCommand(req)
	return err
}

// RemoveDenylist 移除频道内指定用户的黑名单
func (s *StorageWriter) RemoveDenylist(channelID string, channelType uint8, uids []string) error {
	req := transporter.NewCMDReq(CMDRemoveDenylist.Uint32())
	data := EncodeCMDRemoveDenylist(channelID, channelType, uids)
	req.Param = data
	_, err := s.doCommand(req)
	return err
}

// RemoveAllDenylist 移除指定频道的所有黑名单
func (s *StorageWriter) RemoveAllDenylist(channelID string, channelType uint8) error {
	req := transporter.NewCMDReq(CMDRemoveAllDenylist.Uint32())
	data := EncodeCMDRemoveAllDenylist(channelID, channelType)
	req.Param = data
	_, err := s.doCommand(req)
	return err
}

// AddAllowlist 添加白名单
func (s *StorageWriter) AddAllowlist(channelID string, channelType uint8, uids []string) error {
	req := transporter.NewCMDReq(CMDAddAllowlist.Uint32())
	data := EncodeCMDAddAllowlist(channelID, channelType, uids)
	req.Param = data
	_, err := s.doCommand(req)
	return err
}

// RemoveAllowlist 移除白名单
func (s *StorageWriter) RemoveAllowlist(channelID string, channelType uint8, uids []string) error {
	req := transporter.NewCMDReq(CMDRemoveAllowlist.Uint32())
	data := EncodeCMDRemoveAllowlist(channelID, channelType, uids)
	req.Param = data
	_, err := s.doCommand(req)
	return err
}

// RemoveAllAllowlist 移除指定频道的所有白名单
func (s *StorageWriter) RemoveAllAllowlist(channelID string, channelType uint8) error {
	req := transporter.NewCMDReq(CMDRemoveAllAllowlist.Uint32())
	data := EncodeCMDRemoveAllAllowlist(channelID, channelType)
	req.Param = data
	_, err := s.doCommand(req)
	return err
}

// #################### messages ####################
// StoreMsg return seqs and error, seqs len is msgs len
func (s *StorageWriter) AppendMessages(channelID string, channelType uint8, msgs []wkstore.Message) (seqs []uint32, err error) {
	if len(msgs) == 0 {
		return
	}
	req := transporter.NewCMDReq(CMDAppendMessages.Uint32())
	req.Param = EncodeCMDAppendMessages(channelID, channelType, msgs)
	resp, err := s.doCommand(req)
	if err != nil {
		return
	}
	st := Uint32Set{}
	err = st.Decode(resp.Param)
	if err != nil {
		return
	}
	seqs = ([]uint32)(st)
	return
}

// 追加消息到用户的消息队列
func (s *StorageWriter) AppendMessagesOfUser(uid string, msgs []wkstore.Message) (seqs []uint32, err error) {
	if len(msgs) == 0 {
		return
	}
	var (
		resp *transporter.CMDResp
	)
	req := transporter.NewCMDReq(CMDAppendMessagesOfUser.Uint32())
	req.Param = EncodeCMDAppendMessagesOfUser(uid, msgs)
	resp, err = s.doCommand(req)
	if err != nil {
		return
	}
	st := Uint32Set{}
	err = st.Decode(resp.Param)
	if err != nil {
		return
	}
	seqs = ([]uint32)(st)
	return
}
func (s *StorageWriter) AppendMessageOfNotifyQueue(msgs []wkstore.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	req := transporter.NewCMDReq(CMDAppendMessagesOfNotifyQueue.Uint32())
	req.Param = EncodeCMDAppendMessagesOfNotifyQueue(msgs)
	_, err := s.doCommand(req)
	if err != nil {
		return err
	}
	return nil
}

// RemoveMessagesOfNotifyQueue 从通知队列里移除消息
func (s *StorageWriter) RemoveMessagesOfNotifyQueue(messageIDs []int64) error {
	if len(messageIDs) == 0 {
		return nil
	}
	st := Int64Set(messageIDs)
	req := transporter.NewCMDReq(CMDRemoveMessagesOfNotifyQueue.Uint32())
	req.Param = st.Encode()
	_, err := s.doCommand(req)
	if err != nil {
		return err
	}
	return nil
}

func (s *StorageWriter) DeleteChannelAndClearMessages(channelID string, channelType uint8) error {
	req := transporter.NewCMDReq(CMDDeleteChannelAndClearMessages.Uint32())
	req.Param = EncodeCMDDeleteChannelAndClearMessages(channelID, channelType)
	_, err := s.doCommand(req)
	return err
}

// #################### conversations ####################
func (s *StorageWriter) AddOrUpdateConversations(uid string, conversations []*wkstore.Conversation) error {
	if len(conversations) == 0 {
		return nil
	}
	req := transporter.NewCMDReq(CMDAddOrUpdateConversations.Uint32())
	req.Param = EncodeCMDAddOrUpdateConversations(uid, conversations)
	_, err := s.doCommand(req)
	return err
}
func (s *StorageWriter) DeleteConversation(uid string, channelID string, channelType uint8) error {
	req := transporter.NewCMDReq(CMDDeleteConversation.Uint32())
	req.Param = EncodeCMDDeleteConversation(uid, channelID, channelType)
	_, err := s.doCommand(req)
	return err
}

// #################### system uids ####################
func (s *StorageWriter) AddSystemUIDs(uids []string) error {
	req := transporter.NewCMDReq(CMDSystemUIDsAdd.Uint32())
	req.Param = EncodeSystemUIDsAdd(uids)
	_, err := s.doCommand(req)
	return err
}
func (s *StorageWriter) RemoveSystemUIDs(uids []string) error {
	req := transporter.NewCMDReq(CMDSystemUIDsRemove.Uint32())
	req.Param = EncodeSystemUIDsRemove(uids)
	_, err := s.doCommand(req)
	return err
}

// #################### message stream ####################
// SaveStreamMeta 保存消息流元数据
func (s *StorageWriter) SaveStreamMeta(meta *wkstore.StreamMeta) error {
	req := transporter.NewCMDReq(CMDSaveStreamMeta.Uint32())
	req.Param = meta.Encode()
	_, err := s.doCommand(req)
	return err
}

// StreamEnd 结束流
func (s *StorageWriter) StreamEnd(channelID string, channelType uint8, streamNo string) error {
	req := transporter.NewCMDReq(CMDStreamEnd.Uint32())
	req.Param = EncodeCMDStreamEnd(channelID, channelType, streamNo)
	_, err := s.doCommand(req)
	return err
}

// AppendStreamItem 追加消息流
func (s *StorageWriter) AppendStreamItem(channelID string, channelType uint8, streamNo string, item *wkstore.StreamItem) (uint32, error) {
	req := transporter.NewCMDReq(CMDAppendStreamItem.Uint32())
	req.Param = EncodeCMDAppendStreamItem(channelID, channelType, streamNo, item)
	resp, err := s.doCommand(req)
	if err != nil {
		return 0, err
	}
	decoder := wkproto.NewDecoder(resp.Param)
	return decoder.Uint32()
}
