package server

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type FSM struct {
	store wkstore.Store
	wklog.Log
}

func NewFSM(store wkstore.Store) *FSM {

	return &FSM{
		store: store,
		Log:   wklog.NewWKLog("FSM"),
	}
}

func (f *FSM) Apply(req *CMDReq) (*CMDResp, error) {
	r := (*CMDReq)(req)
	switch CMDType(req.Type) {
	case CMDUpdateUserToken:
		return f.applyUpdateUserToken(r)
	case CMDUpdateMessageOfUserCursorIfNeed:
		return f.applyUpdateMessageOfUserCursorIfNeed(r)
	case CMDAddOrUpdateChannel:
		return f.applyAddOrUpdateChannel(r)
	case CMDAddSubscribers:
		return f.applyAddSubscribers(r)
	case CMDRemoveSubscribers:
		return f.applyRemoveSubscribers(r)
	case CMDRemoveAllSubscriber:
		return f.applyRemoveAllSubscriber(r)
	case CMDDeleteChannel:
		return f.applyDeleteChannel(r)
	case CMDAddDenylist:
		return f.applyAddDenylist(r)
	case CMDRemoveDenylist:
		return f.applyRemoveDenylist(r)
	case CMDRemoveAllDenylist:
		return f.applyRemoveAllDenylist(r)
	case CMDAddAllowlist:
		return f.applyAddAllowlist(r)
	case CMDRemoveAllowlist:
		return f.applyRemoveAllowlist(r)
	case CMDRemoveAllAllowlist:
		return f.applyRemoveAllAllowlist(r)
	case CMDAppendMessages:
		return f.applyAppendMessages(r)
	case CMDAppendMessagesOfUser:
		return f.applyAppendMessagesOfUser(r)
	case CMDAppendMessagesOfNotifyQueue:
		return f.applyAppendMessagesOfNotifyQueue(r)
	case CMDRemoveMessagesOfNotifyQueue:
		return f.applyRemoveMessagesOfNotifyQueue(r)
	case CMDDeleteChannelAndClearMessages:
		return f.applyDeleteChannelAndClearMessages(r)
	case CMDAddOrUpdateConversations:
		return f.applyAddOrUpdateConversations(r)
	case CMDDeleteConversation:
		return f.applyDeleteConversation(r)
	case CMDSystemUIDsAdd:
		return f.applySystemUIDsAdd(r)
	case CMDSystemUIDsRemove:
		return f.applySystemUIDsRemove(r)
	case CMDSaveStreamMeta:
		return f.applySaveStreamMeta(r)
	case CMDStreamEnd:
		return f.applyStreamEnd(r)
	case CMDAppendStreamItem:
		return f.applyAppendStreamItem(r)

	}
	return nil, nil
}

func (f *FSM) applyUpdateUserToken(req *CMDReq) (*CMDResp, error) {
	uid, deviceFlag, deviceLevel, token, err := req.DecodeCMDUserToken()
	if err != nil {
		return nil, err
	}
	if err = f.store.UpdateUserToken(uid, deviceFlag, deviceLevel, token); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applyUpdateMessageOfUserCursorIfNeed(req *CMDReq) (*CMDResp, error) {
	uid, messageSeq, err := req.DecodeCMDUpdateMessageOfUserCursorIfNeed()
	if err != nil {
		return nil, err
	}
	if err = f.store.UpdateMessageOfUserCursorIfNeed(uid, messageSeq); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applyAddOrUpdateChannel(req *CMDReq) (*CMDResp, error) {
	channelInfo, err := req.DecodeAddOrUpdateChannel()
	if err != nil {
		return nil, err
	}
	if err = f.store.AddOrUpdateChannel(channelInfo); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applyAddSubscribers(req *CMDReq) (*CMDResp, error) {
	channelID, channelType, uids, err := req.DecodeCMDAddSubscribers()
	if err != nil {
		return nil, err
	}
	if err = f.store.AddSubscribers(channelID, channelType, uids); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applyRemoveSubscribers(req *CMDReq) (*CMDResp, error) {
	channelID, channelType, uids, err := req.DecodeCMDRemoveSubscribers()
	if err != nil {
		return nil, err
	}
	if err = f.store.RemoveSubscribers(channelID, channelType, uids); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applyRemoveAllSubscriber(req *CMDReq) (*CMDResp, error) {
	channelID, channelType, err := req.DecodeCMDRemoveAllSubscriber()
	if err != nil {
		return nil, err
	}
	if err = f.store.RemoveAllSubscriber(channelID, channelType); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applyDeleteChannel(req *CMDReq) (*CMDResp, error) {
	channelID, channelType, err := req.DecodeCMDDeleteChannel()
	if err != nil {
		return nil, err
	}
	if err = f.store.DeleteChannel(channelID, channelType); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applyAddDenylist(req *CMDReq) (*CMDResp, error) {
	channelID, channelType, uids, err := req.DecodeCMDAddDenylist()
	if err != nil {
		return nil, err
	}
	if err = f.store.AddDenylist(channelID, channelType, uids); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applyRemoveDenylist(req *CMDReq) (*CMDResp, error) {
	channelID, channelType, uids, err := req.DecodeCMDRemoveDenylist()
	if err != nil {
		return nil, err
	}
	if err = f.store.RemoveDenylist(channelID, channelType, uids); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applyRemoveAllDenylist(req *CMDReq) (*CMDResp, error) {
	channelID, channelType, err := req.DecodeCMDRemoveAllDenylist()
	if err != nil {
		return nil, err
	}
	if err = f.store.RemoveAllDenylist(channelID, channelType); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applyAddAllowlist(req *CMDReq) (*CMDResp, error) {
	channelID, channelType, uids, err := req.DecodeCMDAddAllowlist()
	if err != nil {
		return nil, err
	}
	if err = f.store.AddAllowlist(channelID, channelType, uids); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applyRemoveAllowlist(req *CMDReq) (*CMDResp, error) {
	channelID, channelType, uids, err := req.DecodeCMDRemoveAllowlist()
	if err != nil {
		return nil, err
	}
	if err = f.store.RemoveAllowlist(channelID, channelType, uids); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applyRemoveAllAllowlist(req *CMDReq) (*CMDResp, error) {
	channelID, channelType, err := req.DecodeCMDRemoveAllAllowlist()
	if err != nil {
		return nil, err
	}
	if err = f.store.RemoveAllAllowlist(channelID, channelType); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applyAppendMessages(req *CMDReq) (*CMDResp, error) {
	channelID, channelType, messages, err := req.DecodeCMDAppendMessages()
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return nil, errors.New("messages is empty")
	}
	seqs, err := f.store.AppendMessages(channelID, channelType, messages)
	if err != nil {
		return nil, err
	}
	st := Uint32Set(seqs)
	return &CMDResp{
		Id:     req.Id,
		Status: CMDRespStatusOK,
		Param:  st.Encode(),
	}, nil
}
func (f *FSM) applyAppendMessagesOfUser(req *CMDReq) (*CMDResp, error) {
	uid, messages, err := req.DecodeCMDAppendMessagesOfUser()
	if err != nil {
		return nil, err
	}
	seqs, err := f.store.AppendMessagesOfUser(uid, messages)
	if err != nil {
		return nil, err
	}
	st := Uint32Set(seqs)
	return &CMDResp{
		Id:     req.Id,
		Status: CMDRespStatusOK,
		Param:  st.Encode(),
	}, nil
}

func (f *FSM) applyAppendMessagesOfNotifyQueue(req *CMDReq) (*CMDResp, error) {
	messages, err := req.DecodeCMDAppendMessagesOfNotifyQueue()
	if err != nil {
		return nil, err
	}
	if err = f.store.AppendMessageOfNotifyQueue(messages); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applyRemoveMessagesOfNotifyQueue(req *CMDReq) (*CMDResp, error) {
	seqs := Int64Set{}
	err := seqs.Decode(req.Param)
	if err != nil {
		return nil, err
	}
	if err = f.store.RemoveMessagesOfNotifyQueue(seqs); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applyDeleteChannelAndClearMessages(req *CMDReq) (*CMDResp, error) {
	channelID, channelType, err := req.DecodeCMDDeleteChannelAndClearMessages()
	if err != nil {
		return nil, err
	}
	if err = f.store.DeleteChannelAndClearMessages(channelID, channelType); err != nil {
		return nil, err
	}
	return nil, nil
}
func (f *FSM) applyAddOrUpdateConversations(req *CMDReq) (*CMDResp, error) {
	uid, conversations, err := req.DecodeCMDAddOrUpdateConversations()
	if err != nil {
		return nil, err
	}
	if err = f.store.AddOrUpdateConversations(uid, conversations); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applyDeleteConversation(req *CMDReq) (*CMDResp, error) {
	uid, channelID, channelType, err := req.DecodeCMDDeleteConversation()
	if err != nil {
		return nil, err
	}
	if err = f.store.DeleteConversation(uid, channelID, channelType); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applySystemUIDsAdd(req *CMDReq) (*CMDResp, error) {
	uids, err := req.DecodeSystemUIDsAdd()
	if err != nil {
		return nil, err
	}
	if err = f.store.AddSystemUIDs(uids); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applySystemUIDsRemove(req *CMDReq) (*CMDResp, error) {
	uids, err := req.DecodeSystemUIDsRemove()
	if err != nil {
		return nil, err
	}
	if err = f.store.RemoveSystemUIDs(uids); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applySaveStreamMeta(req *CMDReq) (*CMDResp, error) {
	streamMeta := &wkstore.StreamMeta{}
	err := streamMeta.Decode(req.Param)
	if err != nil {
		return nil, err
	}
	if err = f.store.SaveStreamMeta(streamMeta); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applyStreamEnd(req *CMDReq) (*CMDResp, error) {
	channelID, channelType, streamNo, err := req.DecodeCMDStreamEnd()
	if err != nil {
		return nil, err
	}
	if err := f.store.StreamEnd(channelID, channelType, streamNo); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FSM) applyAppendStreamItem(req *CMDReq) (*CMDResp, error) {
	channelId, channelType, streamNo, item, err := req.DecodeCMDAppendStreamItem()
	if err != nil {
		return nil, err
	}
	seq, err := f.store.AppendStreamItem(channelId, channelType, streamNo, item)
	if err != nil {
		return nil, err
	}
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteUint32(seq)
	return &CMDResp{
		Id:     req.Id,
		Status: CMDRespStatusOK,
		Param:  encoder.Bytes(),
	}, nil
}
