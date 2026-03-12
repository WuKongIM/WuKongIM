package store

import (
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
)

// AppendMessageEventWithState appends one event through raft proposal and returns persisted event + state.
func (s *Store) AppendMessageEventWithState(channelId string, channelType uint8, event *wkdb.MessageEvent) (*wkdb.MessageEvent, *wkdb.MessageEventState, error) {
	if event == nil {
		return nil, nil, wkdb.ErrNotFound
	}
	if strings.TrimSpace(channelId) == "" {
		return nil, nil, wkdb.ErrNotFound
	}

	// Ensure channel info is set on the event before encoding.
	event.ChannelId = channelId
	event.ChannelType = channelType

	data := EncodeCMDMessageEvent(event, CmdVersionMessageEvent)
	cmd := NewCMDWithVersion(CMDAppendMessageEvent, data, CmdVersionMessageEvent)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return nil, nil, err
	}

	slotID := s.opts.Slot.GetSlotId(channelId)
	_, err = s.opts.Slot.ProposeUntilApplied(slotID, cmdData)
	if err != nil {
		return nil, nil, err
	}

	eventKey := strings.TrimSpace(event.EventKey)
	if eventKey == "" {
		eventKey = wkdb.EventKeyDefault
	}
	eventState, err := s.wdb.GetMessageEventState(channelId, channelType, event.ClientMsgNo, eventKey)
	if err != nil {
		return nil, nil, err
	}
	if eventState == nil {
		return nil, nil, wkdb.ErrNotFound
	}
	stored := *event
	stored.EventKey = eventState.EventKey
	stored.MsgEventSeq = eventState.LastMsgEventSeq
	return &stored, eventState, nil
}

func (s *Store) GetMessageEventByEventID(channelId string, channelType uint8, clientMsgNo, eventID string) (*wkdb.MessageEvent, error) {
	return s.wdb.GetMessageEventByEventID(channelId, channelType, clientMsgNo, eventID)
}

func (s *Store) ListMessageEvents(channelId string, channelType uint8, clientMsgNo string, fromMsgEventSeq uint64, eventKey string, limit int) ([]wkdb.MessageEvent, error) {
	return s.wdb.ListMessageEvents(channelId, channelType, clientMsgNo, fromMsgEventSeq, eventKey, limit)
}

func (s *Store) GetMessageEventStates(channelId string, channelType uint8, clientMsgNo string) ([]wkdb.MessageEventState, error) {
	return s.wdb.GetMessageEventStates(channelId, channelType, clientMsgNo)
}

func (s *Store) GetMessageEventStatesBatch(channelId string, channelType uint8, clientMsgNos []string) (map[string][]wkdb.MessageEventState, error) {
	return s.wdb.GetMessageEventStatesBatch(channelId, channelType, clientMsgNos)
}

func (s *Store) GetMessageEventState(channelId string, channelType uint8, clientMsgNo, eventKey string) (*wkdb.MessageEventState, error) {
	return s.wdb.GetMessageEventState(channelId, channelType, clientMsgNo, eventKey)
}
