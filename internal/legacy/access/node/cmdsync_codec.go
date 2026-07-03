package node

import (
	"fmt"
	"sort"

	"github.com/WuKongIM/WuKongIM/internal/legacy/usecase/cmdsync"
)

var (
	cmdSyncRPCRequestMagic  = [...]byte{'W', 'K', 'M', 'S', 1}
	cmdSyncRPCResponseMagic = [...]byte{'W', 'K', 'M', 'T', 1}
)

func encodeCMDSyncRequestBinary(req cmdSyncRPCRequest) ([]byte, error) {
	dst := make([]byte, 0, len(cmdSyncRPCRequestMagic)+len(req.Op)+len(req.Query.UID)+len(req.Ack.UID)+64)
	dst = append(dst, cmdSyncRPCRequestMagic[:]...)
	dst = appendString(dst, req.Op)
	dst = appendCMDSyncQuery(dst, req.Query)
	dst = appendCMDSyncAckCommand(dst, req.Ack)
	if req.Op == cmdSyncOpPushIntent {
		dst = appendCMDConversationIntent(dst, req.Intent)
	}
	return dst, nil
}

func decodeCMDSyncRequest(body []byte) (cmdSyncRPCRequest, error) {
	if !isCMDSyncRequestBinary(body) {
		return cmdSyncRPCRequest{}, fmt.Errorf("access/node: invalid cmd sync request codec")
	}
	offset := len(cmdSyncRPCRequestMagic)
	var req cmdSyncRPCRequest
	var err error
	if req.Op, offset, err = readString(body, offset); err != nil {
		return cmdSyncRPCRequest{}, err
	}
	if req.Query, offset, err = readCMDSyncQuery(body, offset); err != nil {
		return cmdSyncRPCRequest{}, err
	}
	if req.Ack, offset, err = readCMDSyncAckCommand(body, offset); err != nil {
		return cmdSyncRPCRequest{}, err
	}
	if offset < len(body) {
		if req.Intent, offset, err = readCMDConversationIntent(body, offset); err != nil {
			return cmdSyncRPCRequest{}, err
		}
	}
	if offset != len(body) {
		return cmdSyncRPCRequest{}, fmt.Errorf("access/node: trailing cmd sync request bytes")
	}
	return req, nil
}

func encodeCMDSyncResponse(resp cmdSyncRPCResponse) ([]byte, error) {
	return encodeCMDSyncResponseBinary(resp)
}

func encodeCMDSyncResponseBinary(resp cmdSyncRPCResponse) ([]byte, error) {
	dst := make([]byte, 0, len(cmdSyncRPCResponseMagic)+len(resp.Status)+len(resp.Error)+estimateChannelMessagesBinarySize(resp.Messages)+64)
	dst = append(dst, cmdSyncRPCResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendString(dst, resp.Error)
	dst = appendChannelMessageSlice(dst, resp.Messages)
	return dst, nil
}

func decodeCMDSyncResponse(body []byte) (cmdSyncRPCResponse, error) {
	return decodeCMDSyncResponseBinary(body)
}

func decodeCMDSyncResponseBinary(body []byte) (cmdSyncRPCResponse, error) {
	if !isCMDSyncResponseBinary(body) {
		return cmdSyncRPCResponse{}, fmt.Errorf("access/node: invalid cmd sync response codec")
	}
	offset := len(cmdSyncRPCResponseMagic)
	var resp cmdSyncRPCResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return cmdSyncRPCResponse{}, err
	}
	if resp.Error, offset, err = readString(body, offset); err != nil {
		return cmdSyncRPCResponse{}, err
	}
	if resp.Messages, offset, err = readChannelMessageSlice(body, offset); err != nil {
		return cmdSyncRPCResponse{}, err
	}
	if offset != len(body) {
		return cmdSyncRPCResponse{}, fmt.Errorf("access/node: trailing cmd sync response bytes")
	}
	return resp, nil
}

func isCMDSyncRequestBinary(body []byte) bool {
	return hasMagic(body, cmdSyncRPCRequestMagic[:])
}

func isCMDSyncResponseBinary(body []byte) bool {
	return hasMagic(body, cmdSyncRPCResponseMagic[:])
}

func appendCMDSyncQuery(dst []byte, query cmdsync.SyncQuery) []byte {
	dst = appendString(dst, query.UID)
	dst = appendUvarint(dst, query.MessageSeq)
	return appendNodeInt(dst, query.Limit)
}

func readCMDSyncQuery(body []byte, offset int) (cmdsync.SyncQuery, int, error) {
	var query cmdsync.SyncQuery
	var err error
	if query.UID, offset, err = readString(body, offset); err != nil {
		return cmdsync.SyncQuery{}, offset, err
	}
	if query.MessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return cmdsync.SyncQuery{}, offset, err
	}
	if query.Limit, offset, err = readNodeInt(body, offset, "cmd sync limit"); err != nil {
		return cmdsync.SyncQuery{}, offset, err
	}
	return query, offset, nil
}

func appendCMDSyncAckCommand(dst []byte, cmd cmdsync.SyncAckCommand) []byte {
	dst = appendString(dst, cmd.UID)
	return appendUvarint(dst, cmd.LastMessageSeq)
}

func readCMDSyncAckCommand(body []byte, offset int) (cmdsync.SyncAckCommand, int, error) {
	var cmd cmdsync.SyncAckCommand
	var err error
	if cmd.UID, offset, err = readString(body, offset); err != nil {
		return cmdsync.SyncAckCommand{}, offset, err
	}
	if cmd.LastMessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return cmdsync.SyncAckCommand{}, offset, err
	}
	return cmd, offset, nil
}

func appendCMDConversationIntent(dst []byte, intent cmdsync.ConversationIntent) []byte {
	dst = appendString(dst, intent.CommandChannelID)
	dst = appendUvarint(dst, uint64(intent.ChannelType))
	dst = appendUvarint(dst, intent.MessageSeq)
	dst = appendNodeVarint(dst, intent.ActiveAt)
	dst = appendString(dst, intent.SenderUID)
	keys := make([]string, 0, len(intent.UserReadSeqs))
	for uid := range intent.UserReadSeqs {
		keys = append(keys, uid)
	}
	sort.Strings(keys)
	dst = appendUvarint(dst, uint64(len(keys)))
	for _, uid := range keys {
		dst = appendString(dst, uid)
		dst = appendUvarint(dst, intent.UserReadSeqs[uid])
	}
	return dst
}

func readCMDConversationIntent(body []byte, offset int) (cmdsync.ConversationIntent, int, error) {
	var intent cmdsync.ConversationIntent
	var err error
	if intent.CommandChannelID, offset, err = readString(body, offset); err != nil {
		return cmdsync.ConversationIntent{}, offset, err
	}
	channelType, next, err := readUvarint(body, offset)
	if err != nil {
		return cmdsync.ConversationIntent{}, offset, err
	}
	if channelType > uint64(^uint8(0)) {
		return cmdsync.ConversationIntent{}, offset, fmt.Errorf("access/node: cmd conversation intent channel type overflows uint8")
	}
	intent.ChannelType = uint8(channelType)
	offset = next
	if intent.MessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return cmdsync.ConversationIntent{}, offset, err
	}
	if intent.ActiveAt, offset, err = readNodeVarint(body, offset); err != nil {
		return cmdsync.ConversationIntent{}, offset, err
	}
	if intent.SenderUID, offset, err = readString(body, offset); err != nil {
		return cmdsync.ConversationIntent{}, offset, err
	}
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return cmdsync.ConversationIntent{}, offset, err
	}
	n, err := readCollectionLen(count, len(body)-next, "cmd conversation intent user read seqs")
	if err != nil {
		return cmdsync.ConversationIntent{}, offset, err
	}
	offset = next
	if n > 0 {
		intent.UserReadSeqs = make(map[string]uint64, n)
	}
	for i := 0; i < n; i++ {
		var uid string
		if uid, offset, err = readString(body, offset); err != nil {
			return cmdsync.ConversationIntent{}, offset, err
		}
		var seq uint64
		if seq, offset, err = readUvarint(body, offset); err != nil {
			return cmdsync.ConversationIntent{}, offset, err
		}
		intent.UserReadSeqs[uid] = seq
	}
	return intent, offset, nil
}
