package node

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
)

var (
	senderAuthorityRequestMagic  = [...]byte{'W', 'K', 'V', 'S', 1}
	senderAuthorityResponseMagic = [...]byte{'W', 'K', 'V', 's', 1}
)

const maxSenderAuthorityCollectionLen = 4096

const (
	senderAuthorityErrCodeNone                              = ""
	senderAuthorityErrCodeRejected                          = "rejected"
	senderAuthorityErrCodeNotLeader                         = "not_leader"
	senderAuthorityErrCodeStaleRoute                        = "stale_route"
	senderAuthorityErrCodeRouteNotReady                     = "route_not_ready"
	senderAuthorityErrCodeChannelNotFound                   = "channel_not_found"
	senderAuthorityErrCodeBackpressured                     = "backpressured"
	senderAuthorityErrCodeAppendResultMissing               = "append_result_missing"
	senderAuthorityErrCodeAppendFailed                      = "append_failed"
	senderAuthorityErrCodeInvalidCommand                    = "invalid_command"
	senderAuthorityErrCodeAppenderRequired                  = "appender_required"
	senderAuthorityErrCodeMessageIDAllocatorRequired        = "message_id_allocator_required"
	senderAuthorityErrCodeRequestSubscribersRequireSyncOnce = "request_subscribers_require_sync_once"
	senderAuthorityErrCodeRequestSubscribersConflictChannel = "request_subscribers_conflict_channel"
	senderAuthorityErrCodeRequestSubscribersRequired        = "request_subscribers_required"
	senderAuthorityErrCodeContextCanceled                   = "context_canceled"
	senderAuthorityErrCodeContextDeadlineExceeded           = "context_deadline_exceeded"
)

// senderAuthorityItem is one send command plus the relative timeout transported over RPC.
type senderAuthorityItem struct {
	// Command is the entry-agnostic SEND command.
	Command message.SendCommand
	// Timeout is the relative item deadline encoded for the authority node.
	Timeout time.Duration
}

// senderAuthorityRequest is the deterministic binary DTO for sender authority calls.
type senderAuthorityRequest struct {
	// Target fences the request to one observed sender UID authority epoch.
	Target authority.Target
	// Items carries item-aligned send commands for the sender authority.
	Items []senderAuthorityItem
}

// senderAuthorityResponse is the deterministic binary DTO returned by sender authority calls.
type senderAuthorityResponse struct {
	// Status is the envelope-level RPC status.
	Status string
	// Results carries item-aligned SEND outcomes when Status is ok.
	Results []message.SendBatchItemResult
}

func encodeSenderAuthorityRequest(req senderAuthorityRequest) ([]byte, error) {
	dst := make([]byte, 0, 128)
	dst = append(dst, senderAuthorityRequestMagic[:]...)
	dst = appendAuthorityTarget(dst, req.Target)
	dst = appendSenderAuthorityItems(dst, req.Items)
	return dst, nil
}

func decodeSenderAuthorityRequest(body []byte) (senderAuthorityRequest, error) {
	if !hasMagic(body, senderAuthorityRequestMagic[:]) {
		return senderAuthorityRequest{}, fmt.Errorf("internalv2/access/node: invalid sender authority request codec")
	}
	offset := len(senderAuthorityRequestMagic)
	var req senderAuthorityRequest
	var err error
	if req.Target, offset, err = readAuthorityTarget(body, offset); err != nil {
		return senderAuthorityRequest{}, err
	}
	if req.Items, offset, err = readSenderAuthorityItems(body, offset); err != nil {
		return senderAuthorityRequest{}, err
	}
	if offset != len(body) {
		return senderAuthorityRequest{}, fmt.Errorf("internalv2/access/node: trailing sender authority request bytes")
	}
	return req, nil
}

func encodeSenderAuthorityResponse(resp senderAuthorityResponse) ([]byte, error) {
	dst := make([]byte, 0, 128)
	dst = append(dst, senderAuthorityResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendSenderAuthorityResults(dst, resp.Results)
	return dst, nil
}

func decodeSenderAuthorityResponse(body []byte) (senderAuthorityResponse, error) {
	if !hasMagic(body, senderAuthorityResponseMagic[:]) {
		return senderAuthorityResponse{}, fmt.Errorf("internalv2/access/node: invalid sender authority response codec")
	}
	offset := len(senderAuthorityResponseMagic)
	var resp senderAuthorityResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return senderAuthorityResponse{}, err
	}
	if resp.Results, offset, err = readSenderAuthorityResults(body, offset); err != nil {
		return senderAuthorityResponse{}, err
	}
	if offset != len(body) {
		return senderAuthorityResponse{}, fmt.Errorf("internalv2/access/node: trailing sender authority response bytes")
	}
	return resp, nil
}

func appendAuthorityTarget(dst []byte, target authority.Target) []byte {
	dst = appendUvarint(dst, uint64(target.HashSlot))
	dst = appendUvarint(dst, uint64(target.SlotID))
	dst = appendUvarint(dst, target.LeaderNodeID)
	dst = appendUvarint(dst, target.RouteRevision)
	return appendUvarint(dst, target.AuthorityEpoch)
}

func readAuthorityTarget(body []byte, offset int) (authority.Target, int, error) {
	var target authority.Target
	var v uint64
	var err error
	if v, offset, err = readUvarint(body, offset); err != nil {
		return authority.Target{}, offset, err
	}
	if v > uint64(^uint16(0)) {
		return authority.Target{}, offset, fmt.Errorf("internalv2/access/node: sender authority hash slot overflows uint16")
	}
	target.HashSlot = uint16(v)
	if v, offset, err = readUvarint(body, offset); err != nil {
		return authority.Target{}, offset, err
	}
	if v > uint64(^uint32(0)) {
		return authority.Target{}, offset, fmt.Errorf("internalv2/access/node: sender authority slot id overflows uint32")
	}
	target.SlotID = uint32(v)
	if target.LeaderNodeID, offset, err = readUvarint(body, offset); err != nil {
		return authority.Target{}, offset, err
	}
	if target.RouteRevision, offset, err = readUvarint(body, offset); err != nil {
		return authority.Target{}, offset, err
	}
	if target.AuthorityEpoch, offset, err = readUvarint(body, offset); err != nil {
		return authority.Target{}, offset, err
	}
	return target, offset, nil
}

func appendSenderAuthorityItems(dst []byte, items []senderAuthorityItem) []byte {
	dst = appendUvarint(dst, uint64(len(items)))
	for _, item := range items {
		dst = appendSenderAuthorityItem(dst, item)
	}
	return dst
}

func readSenderAuthorityItems(body []byte, offset int) ([]senderAuthorityItem, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, nil
	}
	if err := validateSenderAuthorityCollectionLen(count, len(body)-offset, "sender authority items"); err != nil {
		return nil, offset, err
	}
	items := make([]senderAuthorityItem, 0, int(count))
	for i := uint64(0); i < count; i++ {
		var item senderAuthorityItem
		if item, offset, err = readSenderAuthorityItem(body, offset); err != nil {
			return nil, offset, err
		}
		items = append(items, item)
	}
	return items, offset, nil
}

func appendSenderAuthorityItem(dst []byte, item senderAuthorityItem) []byte {
	dst = appendMessageSendCommand(dst, item.Command)
	return appendVarint(dst, int64(item.Timeout))
}

func readSenderAuthorityItem(body []byte, offset int) (senderAuthorityItem, int, error) {
	var item senderAuthorityItem
	var timeout int64
	var err error
	if item.Command, offset, err = readMessageSendCommand(body, offset); err != nil {
		return senderAuthorityItem{}, offset, err
	}
	if timeout, offset, err = readVarint(body, offset); err != nil {
		return senderAuthorityItem{}, offset, err
	}
	if timeout < 0 {
		return senderAuthorityItem{}, offset, fmt.Errorf("internalv2/access/node: sender authority timeout is negative")
	}
	item.Timeout = time.Duration(timeout)
	return item, offset, nil
}

func appendMessageSendCommand(dst []byte, cmd message.SendCommand) []byte {
	dst = appendString(dst, cmd.FromUID)
	dst = appendUvarint(dst, cmd.SenderNodeID)
	dst = appendUvarint(dst, cmd.SenderSessionID)
	dst = appendUvarint(dst, cmd.ClientSeq)
	dst = appendString(dst, cmd.ClientMsgNo)
	dst = appendString(dst, cmd.TraceID)
	dst = appendString(dst, cmd.ChannelKey)
	dst = appendString(dst, cmd.ChannelID)
	dst = append(dst, cmd.ChannelType)
	dst = appendBytes(dst, cmd.Payload)
	dst = appendSenderAuthorityBool(dst, cmd.NoPersist)
	dst = appendSenderAuthorityBool(dst, cmd.SyncOnce)
	dst = appendSenderAuthorityBool(dst, cmd.RedDot)
	dst = appendSenderAuthorityBool(dst, cmd.NormalizePersonChannel)
	dst = appendSenderAuthorityBool(dst, cmd.RequestScoped)
	dst = appendSenderAuthorityStringSlice(dst, cmd.MessageScopedUIDs)
	dst = appendUvarint(dst, cmd.MessageID)
	return append(dst, cmd.ProtocolVersion)
}

func readMessageSendCommand(body []byte, offset int) (message.SendCommand, int, error) {
	var cmd message.SendCommand
	var err error
	if cmd.FromUID, offset, err = readString(body, offset); err != nil {
		return message.SendCommand{}, offset, err
	}
	if cmd.SenderNodeID, offset, err = readUvarint(body, offset); err != nil {
		return message.SendCommand{}, offset, err
	}
	if cmd.SenderSessionID, offset, err = readUvarint(body, offset); err != nil {
		return message.SendCommand{}, offset, err
	}
	if cmd.ClientSeq, offset, err = readUvarint(body, offset); err != nil {
		return message.SendCommand{}, offset, err
	}
	if cmd.ClientMsgNo, offset, err = readString(body, offset); err != nil {
		return message.SendCommand{}, offset, err
	}
	if cmd.TraceID, offset, err = readString(body, offset); err != nil {
		return message.SendCommand{}, offset, err
	}
	if cmd.ChannelKey, offset, err = readString(body, offset); err != nil {
		return message.SendCommand{}, offset, err
	}
	if cmd.ChannelID, offset, err = readString(body, offset); err != nil {
		return message.SendCommand{}, offset, err
	}
	if cmd.ChannelType, offset, err = readByte(body, offset, "sender authority channel type"); err != nil {
		return message.SendCommand{}, offset, err
	}
	if cmd.Payload, offset, err = readBytes(body, offset); err != nil {
		return message.SendCommand{}, offset, err
	}
	if cmd.NoPersist, offset, err = readSenderAuthorityBool(body, offset, "sender authority no persist"); err != nil {
		return message.SendCommand{}, offset, err
	}
	if cmd.SyncOnce, offset, err = readSenderAuthorityBool(body, offset, "sender authority sync once"); err != nil {
		return message.SendCommand{}, offset, err
	}
	if cmd.RedDot, offset, err = readSenderAuthorityBool(body, offset, "sender authority red dot"); err != nil {
		return message.SendCommand{}, offset, err
	}
	if cmd.NormalizePersonChannel, offset, err = readSenderAuthorityBool(body, offset, "sender authority normalize person channel"); err != nil {
		return message.SendCommand{}, offset, err
	}
	if cmd.RequestScoped, offset, err = readSenderAuthorityBool(body, offset, "sender authority request scoped"); err != nil {
		return message.SendCommand{}, offset, err
	}
	if cmd.MessageScopedUIDs, offset, err = readSenderAuthorityStringSlice(body, offset, "sender authority message scoped uids"); err != nil {
		return message.SendCommand{}, offset, err
	}
	if cmd.MessageID, offset, err = readUvarint(body, offset); err != nil {
		return message.SendCommand{}, offset, err
	}
	if cmd.ProtocolVersion, offset, err = readByte(body, offset, "sender authority protocol version"); err != nil {
		return message.SendCommand{}, offset, err
	}
	return cmd, offset, nil
}

func appendSenderAuthorityResults(dst []byte, results []message.SendBatchItemResult) []byte {
	dst = appendUvarint(dst, uint64(len(results)))
	for _, result := range results {
		dst = appendSenderAuthorityResult(dst, result)
	}
	return dst
}

func readSenderAuthorityResults(body []byte, offset int) ([]message.SendBatchItemResult, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, nil
	}
	if err := validateSenderAuthorityCollectionLen(count, len(body)-offset, "sender authority results"); err != nil {
		return nil, offset, err
	}
	results := make([]message.SendBatchItemResult, 0, int(count))
	for i := uint64(0); i < count; i++ {
		var result message.SendBatchItemResult
		if result, offset, err = readSenderAuthorityResult(body, offset); err != nil {
			return nil, offset, err
		}
		results = append(results, result)
	}
	return results, offset, nil
}

func appendSenderAuthorityResult(dst []byte, result message.SendBatchItemResult) []byte {
	dst = appendSenderAuthoritySendResult(dst, result.Result)
	return appendSenderAuthorityResultError(dst, result.Err)
}

func readSenderAuthorityResult(body []byte, offset int) (message.SendBatchItemResult, int, error) {
	var result message.SendBatchItemResult
	var err error
	if result.Result, offset, err = readSenderAuthoritySendResult(body, offset); err != nil {
		return message.SendBatchItemResult{}, offset, err
	}
	if result.Err, offset, err = readSenderAuthorityResultError(body, offset); err != nil {
		return message.SendBatchItemResult{}, offset, err
	}
	return result, offset, nil
}

func appendSenderAuthoritySendResult(dst []byte, result message.SendResult) []byte {
	dst = appendUvarint(dst, result.MessageID)
	dst = appendUvarint(dst, result.MessageSeq)
	return append(dst, byte(result.Reason))
}

func readSenderAuthoritySendResult(body []byte, offset int) (message.SendResult, int, error) {
	var result message.SendResult
	var reason byte
	var err error
	if result.MessageID, offset, err = readUvarint(body, offset); err != nil {
		return message.SendResult{}, offset, err
	}
	if result.MessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return message.SendResult{}, offset, err
	}
	if reason, offset, err = readByte(body, offset, "sender authority result reason"); err != nil {
		return message.SendResult{}, offset, err
	}
	result.Reason = message.Reason(reason)
	return result, offset, nil
}

func appendSenderAuthorityResultError(dst []byte, err error) []byte {
	code := senderAuthorityErrorCode(err)
	dst = appendString(dst, code)
	if err == nil {
		return appendString(dst, "")
	}
	return appendString(dst, err.Error())
}

func readSenderAuthorityResultError(body []byte, offset int) (error, int, error) {
	code, next, err := readString(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	msg, next, err := readString(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	switch code {
	case senderAuthorityErrCodeNone:
		return nil, offset, nil
	case senderAuthorityErrCodeNotLeader:
		return message.ErrNotLeader, offset, nil
	case senderAuthorityErrCodeStaleRoute:
		return message.ErrStaleRoute, offset, nil
	case senderAuthorityErrCodeRouteNotReady:
		return message.ErrRouteNotReady, offset, nil
	case senderAuthorityErrCodeChannelNotFound:
		return message.ErrChannelNotFound, offset, nil
	case senderAuthorityErrCodeBackpressured:
		return message.ErrBackpressured, offset, nil
	case senderAuthorityErrCodeAppendResultMissing:
		return message.ErrAppendResultMissing, offset, nil
	case senderAuthorityErrCodeAppendFailed:
		return message.ErrAppendFailed, offset, nil
	case senderAuthorityErrCodeInvalidCommand:
		return message.ErrInvalidCommand, offset, nil
	case senderAuthorityErrCodeAppenderRequired:
		return message.ErrAppenderRequired, offset, nil
	case senderAuthorityErrCodeMessageIDAllocatorRequired:
		return message.ErrMessageIDAllocatorRequired, offset, nil
	case senderAuthorityErrCodeRequestSubscribersRequireSyncOnce:
		return message.ErrRequestSubscribersRequireSyncOnce, offset, nil
	case senderAuthorityErrCodeRequestSubscribersConflictChannel:
		return message.ErrRequestSubscribersConflictChannel, offset, nil
	case senderAuthorityErrCodeRequestSubscribersRequired:
		return message.ErrRequestSubscribersRequired, offset, nil
	case senderAuthorityErrCodeContextCanceled:
		return context.Canceled, offset, nil
	case senderAuthorityErrCodeContextDeadlineExceeded:
		return context.DeadlineExceeded, offset, nil
	case senderAuthorityErrCodeRejected:
		if msg == "" {
			msg = "sender authority result rejected"
		}
		return fmt.Errorf("internalv2/access/node: %s", msg), offset, nil
	default:
		return nil, offset, fmt.Errorf("internalv2/access/node: unknown sender authority result error code %q", code)
	}
}

func senderAuthorityErrorCode(err error) string {
	switch {
	case err == nil:
		return senderAuthorityErrCodeNone
	case errors.Is(err, message.ErrNotLeader):
		return senderAuthorityErrCodeNotLeader
	case errors.Is(err, message.ErrStaleRoute):
		return senderAuthorityErrCodeStaleRoute
	case errors.Is(err, message.ErrRouteNotReady):
		return senderAuthorityErrCodeRouteNotReady
	case errors.Is(err, message.ErrChannelNotFound):
		return senderAuthorityErrCodeChannelNotFound
	case errors.Is(err, message.ErrBackpressured):
		return senderAuthorityErrCodeBackpressured
	case errors.Is(err, message.ErrAppendResultMissing):
		return senderAuthorityErrCodeAppendResultMissing
	case errors.Is(err, message.ErrAppendFailed):
		return senderAuthorityErrCodeAppendFailed
	case errors.Is(err, message.ErrInvalidCommand):
		return senderAuthorityErrCodeInvalidCommand
	case errors.Is(err, message.ErrAppenderRequired):
		return senderAuthorityErrCodeAppenderRequired
	case errors.Is(err, message.ErrMessageIDAllocatorRequired):
		return senderAuthorityErrCodeMessageIDAllocatorRequired
	case errors.Is(err, message.ErrRequestSubscribersRequireSyncOnce):
		return senderAuthorityErrCodeRequestSubscribersRequireSyncOnce
	case errors.Is(err, message.ErrRequestSubscribersConflictChannel):
		return senderAuthorityErrCodeRequestSubscribersConflictChannel
	case errors.Is(err, message.ErrRequestSubscribersRequired):
		return senderAuthorityErrCodeRequestSubscribersRequired
	case errors.Is(err, context.Canceled):
		return senderAuthorityErrCodeContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return senderAuthorityErrCodeContextDeadlineExceeded
	default:
		return senderAuthorityErrCodeRejected
	}
}

func appendSenderAuthorityStringSlice(dst []byte, values []string) []byte {
	dst = appendUvarint(dst, uint64(len(values)))
	for _, value := range values {
		dst = appendString(dst, value)
	}
	return dst
}

func readSenderAuthorityStringSlice(body []byte, offset int, label string) ([]string, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, nil
	}
	if err := validateSenderAuthorityCollectionLen(count, len(body)-offset, label); err != nil {
		return nil, offset, err
	}
	values := make([]string, 0, int(count))
	for i := uint64(0); i < count; i++ {
		var value string
		if value, offset, err = readString(body, offset); err != nil {
			return nil, offset, err
		}
		values = append(values, value)
	}
	return values, offset, nil
}

func appendSenderAuthorityBool(dst []byte, value bool) []byte {
	if value {
		return append(dst, 1)
	}
	return append(dst, 0)
}

func readSenderAuthorityBool(body []byte, offset int, label string) (bool, int, error) {
	v, next, err := readByte(body, offset, label)
	if err != nil {
		return false, offset, err
	}
	switch v {
	case 0:
		return false, next, nil
	case 1:
		return true, next, nil
	default:
		return false, offset, fmt.Errorf("internalv2/access/node: invalid %s flag", label)
	}
}

func validateSenderAuthorityCollectionLen(count uint64, remaining int, label string) error {
	if count > maxSenderAuthorityCollectionLen {
		return fmt.Errorf("internalv2/access/node: %s length exceeds limit", label)
	}
	if count > uint64(remaining) {
		return fmt.Errorf("internalv2/access/node: %s length exceeds payload", label)
	}
	return nil
}
