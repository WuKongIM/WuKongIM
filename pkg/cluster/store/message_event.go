package store

import (
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
)

// AppendMessageEventWithLaneState appends one event through raft proposal and returns persisted event + lane state.
func (s *Store) AppendMessageEventWithLaneState(channelId string, channelType uint8, event *wkdb.MessageEvent) (*wkdb.MessageEvent, *wkdb.MessageLaneState, error) {
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

	laneID := strings.TrimSpace(event.LaneID)
	if laneID == "" {
		laneID = "main"
	}
	laneState, err := s.wdb.GetMessageLaneState(channelId, channelType, event.ClientMsgNo, laneID)
	if err != nil {
		return nil, nil, err
	}
	if laneState == nil {
		return nil, nil, wkdb.ErrNotFound
	}
	stored := *event
	stored.LaneID = laneState.LaneID
	stored.MsgEventSeq = laneState.LastMsgEventSeq
	return &stored, laneState, nil
}

func (s *Store) GetMessageEventByEventID(channelId string, channelType uint8, clientMsgNo, eventID string) (*wkdb.MessageEvent, error) {
	return s.wdb.GetMessageEventByEventID(channelId, channelType, clientMsgNo, eventID)
}

func (s *Store) ListMessageEvents(channelId string, channelType uint8, clientMsgNo string, fromMsgEventSeq uint64, laneID string, limit int) ([]wkdb.MessageEvent, error) {
	return s.wdb.ListMessageEvents(channelId, channelType, clientMsgNo, fromMsgEventSeq, laneID, limit)
}

func (s *Store) GetMessageLaneStates(channelId string, channelType uint8, clientMsgNo string) ([]wkdb.MessageLaneState, error) {
	return s.wdb.GetMessageLaneStates(channelId, channelType, clientMsgNo)
}

func (s *Store) GetMessageLaneStatesBatch(channelId string, channelType uint8, clientMsgNos []string) (map[string][]wkdb.MessageLaneState, error) {
	return s.wdb.GetMessageLaneStatesBatch(channelId, channelType, clientMsgNos)
}

func (s *Store) GetMessageLaneState(channelId string, channelType uint8, clientMsgNo, laneID string) (*wkdb.MessageLaneState, error) {
	return s.wdb.GetMessageLaneState(channelId, channelType, clientMsgNo, laneID)
}
