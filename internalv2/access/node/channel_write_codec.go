package node

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/channelwrite"
)

var (
	channelWriteRequestMagic  = [...]byte{'W', 'K', 'V', 'W', 2}
	channelWriteResponseMagic = [...]byte{'W', 'K', 'V', 'w', 1}
)

const maxChannelWriteCollectionLen = 4096

const (
	channelWriteErrCodeNotLeader               = "not_leader"
	channelWriteErrCodeStaleRoute              = "stale_route"
	channelWriteErrCodeRouteNotReady           = "route_not_ready"
	channelWriteErrCodeChannelBusy             = "channel_busy"
	channelWriteErrCodeContextCanceled         = "context_canceled"
	channelWriteErrCodeContextDeadlineExceeded = "context_deadline_exceeded"
	channelWriteErrCodeRejected                = "rejected"
	channelWriteErrCodeNotChannelAuthority     = "not_channel_authority"
	channelWriteErrCodeBackpressured           = "backpressured"
	channelWriteErrCodeAppendResultMissing     = "append_result_missing"
)

// channelWriteItem is one send command plus the relative timeout transported over RPC.
type channelWriteItem struct {
	// Command is the entry-agnostic SEND command.
	Command channelwrite.SendCommand
	// Timeout is the relative item deadline encoded for the authority node.
	Timeout time.Duration
}

// channelWriteRequest is the deterministic binary DTO for channel write calls.
type channelWriteRequest struct {
	// Target fences the request to one observed channel authority.
	Target channelwrite.AuthorityTarget
	// Items carries item-aligned send commands for the channel authority.
	Items []channelWriteItem
}

// channelWriteResponse is the deterministic binary DTO returned by channel write calls.
type channelWriteResponse struct {
	// Status is the envelope-level RPC status.
	Status string
	// Results carries item-aligned SEND outcomes when Status is ok.
	Results []channelwrite.SendBatchItemResult
}

func encodeChannelWriteRequest(req channelWriteRequest) ([]byte, error) {
	dst := make([]byte, 0, 128)
	dst = append(dst, channelWriteRequestMagic[:]...)
	dst = appendChannelWriteTarget(dst, req.Target)
	dst = appendChannelWriteItems(dst, req.Items)
	return dst, nil
}

func decodeChannelWriteRequest(body []byte) (channelWriteRequest, error) {
	if !hasMagic(body, channelWriteRequestMagic[:]) {
		return channelWriteRequest{}, fmt.Errorf("internalv2/access/node: invalid channel write request codec")
	}
	offset := len(channelWriteRequestMagic)
	var req channelWriteRequest
	var err error
	if req.Target, offset, err = readChannelWriteTarget(body, offset); err != nil {
		return channelWriteRequest{}, err
	}
	if req.Items, offset, err = readChannelWriteItems(body, offset); err != nil {
		return channelWriteRequest{}, err
	}
	if offset != len(body) {
		return channelWriteRequest{}, fmt.Errorf("internalv2/access/node: trailing channel write request bytes")
	}
	return req, nil
}

func encodeChannelWriteResponse(resp channelWriteResponse) ([]byte, error) {
	dst := make([]byte, 0, 128)
	dst = append(dst, channelWriteResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendChannelWriteResults(dst, resp.Results)
	return dst, nil
}

func decodeChannelWriteResponse(body []byte) (channelWriteResponse, error) {
	if !hasMagic(body, channelWriteResponseMagic[:]) {
		return channelWriteResponse{}, fmt.Errorf("internalv2/access/node: invalid channel write response codec")
	}
	offset := len(channelWriteResponseMagic)
	var resp channelWriteResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return channelWriteResponse{}, err
	}
	if resp.Results, offset, err = readChannelWriteResults(body, offset); err != nil {
		return channelWriteResponse{}, err
	}
	if offset != len(body) {
		return channelWriteResponse{}, fmt.Errorf("internalv2/access/node: trailing channel write response bytes")
	}
	return resp, nil
}

func appendChannelWriteTarget(dst []byte, target channelwrite.AuthorityTarget) []byte {
	dst = appendString(dst, target.ChannelID.ID)
	dst = append(dst, target.ChannelID.Type)
	dst = appendString(dst, target.ChannelKey)
	dst = appendUvarint(dst, target.LeaderNodeID)
	dst = appendUvarint(dst, target.Epoch)
	dst = appendUvarint(dst, target.LeaderEpoch)
	dst = appendUvarint(dst, target.RouteRevision)
	dst = appendChannelWriteBool(dst, target.Large)
	return appendUvarint(dst, target.SubscriberMutationVersion)
}

func readChannelWriteTarget(body []byte, offset int) (channelwrite.AuthorityTarget, int, error) {
	var target channelwrite.AuthorityTarget
	var err error
	if target.ChannelID.ID, offset, err = readString(body, offset); err != nil {
		return channelwrite.AuthorityTarget{}, offset, err
	}
	if target.ChannelID.Type, offset, err = readByte(body, offset, "channel write target channel type"); err != nil {
		return channelwrite.AuthorityTarget{}, offset, err
	}
	if target.ChannelKey, offset, err = readString(body, offset); err != nil {
		return channelwrite.AuthorityTarget{}, offset, err
	}
	if target.LeaderNodeID, offset, err = readUvarint(body, offset); err != nil {
		return channelwrite.AuthorityTarget{}, offset, err
	}
	if target.Epoch, offset, err = readUvarint(body, offset); err != nil {
		return channelwrite.AuthorityTarget{}, offset, err
	}
	if target.LeaderEpoch, offset, err = readUvarint(body, offset); err != nil {
		return channelwrite.AuthorityTarget{}, offset, err
	}
	if target.RouteRevision, offset, err = readUvarint(body, offset); err != nil {
		return channelwrite.AuthorityTarget{}, offset, err
	}
	if target.Large, offset, err = readChannelWriteBool(body, offset, "channel write target large"); err != nil {
		return channelwrite.AuthorityTarget{}, offset, err
	}
	if target.SubscriberMutationVersion, offset, err = readUvarint(body, offset); err != nil {
		return channelwrite.AuthorityTarget{}, offset, err
	}
	return target, offset, nil
}

func appendChannelWriteItems(dst []byte, items []channelWriteItem) []byte {
	dst = appendUvarint(dst, uint64(len(items)))
	for _, item := range items {
		dst = appendChannelWriteItem(dst, item)
	}
	return dst
}

func readChannelWriteItems(body []byte, offset int) ([]channelWriteItem, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, fmt.Errorf("internalv2/access/node: empty channel write request")
	}
	if err := validateChannelWriteCollectionLen(count, len(body)-offset, "channel write items"); err != nil {
		return nil, offset, err
	}
	items := make([]channelWriteItem, 0, int(count))
	for i := uint64(0); i < count; i++ {
		var item channelWriteItem
		if item, offset, err = readChannelWriteItem(body, offset); err != nil {
			return nil, offset, err
		}
		items = append(items, item)
	}
	return items, offset, nil
}

func appendChannelWriteItem(dst []byte, item channelWriteItem) []byte {
	dst = appendChannelWriteSendCommand(dst, item.Command)
	return appendVarint(dst, int64(item.Timeout))
}

func readChannelWriteItem(body []byte, offset int) (channelWriteItem, int, error) {
	var item channelWriteItem
	var timeout int64
	var err error
	if item.Command, offset, err = readChannelWriteSendCommand(body, offset); err != nil {
		return channelWriteItem{}, offset, err
	}
	if timeout, offset, err = readVarint(body, offset); err != nil {
		return channelWriteItem{}, offset, err
	}
	if timeout < 0 {
		return channelWriteItem{}, offset, fmt.Errorf("internalv2/access/node: channel write timeout is negative")
	}
	item.Timeout = time.Duration(timeout)
	return item, offset, nil
}

func appendChannelWriteSendCommand(dst []byte, cmd channelwrite.SendCommand) []byte {
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
	dst = appendChannelWriteBool(dst, cmd.NoPersist)
	dst = appendChannelWriteBool(dst, cmd.SyncOnce)
	dst = appendChannelWriteBool(dst, cmd.RedDot)
	dst = appendChannelWriteBool(dst, cmd.NormalizePersonChannel)
	dst = appendChannelWriteBool(dst, cmd.RequestScoped)
	dst = appendChannelWriteStringSlice(dst, cmd.MessageScopedUIDs)
	dst = appendUvarint(dst, cmd.MessageID)
	return append(dst, cmd.ProtocolVersion)
}

func readChannelWriteSendCommand(body []byte, offset int) (channelwrite.SendCommand, int, error) {
	var cmd channelwrite.SendCommand
	var err error
	if cmd.FromUID, offset, err = readString(body, offset); err != nil {
		return channelwrite.SendCommand{}, offset, err
	}
	if cmd.SenderNodeID, offset, err = readUvarint(body, offset); err != nil {
		return channelwrite.SendCommand{}, offset, err
	}
	if cmd.SenderSessionID, offset, err = readUvarint(body, offset); err != nil {
		return channelwrite.SendCommand{}, offset, err
	}
	if cmd.ClientSeq, offset, err = readUvarint(body, offset); err != nil {
		return channelwrite.SendCommand{}, offset, err
	}
	if cmd.ClientMsgNo, offset, err = readString(body, offset); err != nil {
		return channelwrite.SendCommand{}, offset, err
	}
	if cmd.TraceID, offset, err = readString(body, offset); err != nil {
		return channelwrite.SendCommand{}, offset, err
	}
	if cmd.ChannelKey, offset, err = readString(body, offset); err != nil {
		return channelwrite.SendCommand{}, offset, err
	}
	if cmd.ChannelID, offset, err = readString(body, offset); err != nil {
		return channelwrite.SendCommand{}, offset, err
	}
	if cmd.ChannelType, offset, err = readByte(body, offset, "channel write command channel type"); err != nil {
		return channelwrite.SendCommand{}, offset, err
	}
	if cmd.Payload, offset, err = readBytes(body, offset); err != nil {
		return channelwrite.SendCommand{}, offset, err
	}
	if cmd.NoPersist, offset, err = readChannelWriteBool(body, offset, "channel write no persist"); err != nil {
		return channelwrite.SendCommand{}, offset, err
	}
	if cmd.SyncOnce, offset, err = readChannelWriteBool(body, offset, "channel write sync once"); err != nil {
		return channelwrite.SendCommand{}, offset, err
	}
	if cmd.RedDot, offset, err = readChannelWriteBool(body, offset, "channel write red dot"); err != nil {
		return channelwrite.SendCommand{}, offset, err
	}
	if cmd.NormalizePersonChannel, offset, err = readChannelWriteBool(body, offset, "channel write normalize person channel"); err != nil {
		return channelwrite.SendCommand{}, offset, err
	}
	if cmd.RequestScoped, offset, err = readChannelWriteBool(body, offset, "channel write request scoped"); err != nil {
		return channelwrite.SendCommand{}, offset, err
	}
	if cmd.MessageScopedUIDs, offset, err = readChannelWriteStringSlice(body, offset, "channel write message scoped uids"); err != nil {
		return channelwrite.SendCommand{}, offset, err
	}
	if cmd.MessageID, offset, err = readUvarint(body, offset); err != nil {
		return channelwrite.SendCommand{}, offset, err
	}
	if cmd.ProtocolVersion, offset, err = readByte(body, offset, "channel write protocol version"); err != nil {
		return channelwrite.SendCommand{}, offset, err
	}
	return cmd, offset, nil
}

func appendChannelWriteResults(dst []byte, results []channelwrite.SendBatchItemResult) []byte {
	dst = appendUvarint(dst, uint64(len(results)))
	for _, result := range results {
		dst = appendChannelWriteResult(dst, result)
	}
	return dst
}

func readChannelWriteResults(body []byte, offset int) ([]channelwrite.SendBatchItemResult, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, nil
	}
	if err := validateChannelWriteCollectionLen(count, len(body)-offset, "channel write results"); err != nil {
		return nil, offset, err
	}
	results := make([]channelwrite.SendBatchItemResult, 0, int(count))
	for i := uint64(0); i < count; i++ {
		var result channelwrite.SendBatchItemResult
		if result, offset, err = readChannelWriteResult(body, offset); err != nil {
			return nil, offset, err
		}
		results = append(results, result)
	}
	return results, offset, nil
}

func appendChannelWriteResult(dst []byte, result channelwrite.SendBatchItemResult) []byte {
	dst = appendChannelWriteSendResult(dst, result.Result)
	return appendChannelWriteResultError(dst, result.Err)
}

func readChannelWriteResult(body []byte, offset int) (channelwrite.SendBatchItemResult, int, error) {
	var result channelwrite.SendBatchItemResult
	var err error
	if result.Result, offset, err = readChannelWriteSendResult(body, offset); err != nil {
		return channelwrite.SendBatchItemResult{}, offset, err
	}
	if result.Err, offset, err = readChannelWriteResultError(body, offset); err != nil {
		return channelwrite.SendBatchItemResult{}, offset, err
	}
	return result, offset, nil
}

func appendChannelWriteSendResult(dst []byte, result channelwrite.SendResult) []byte {
	dst = appendUvarint(dst, result.MessageID)
	dst = appendUvarint(dst, result.MessageSeq)
	return append(dst, byte(result.Reason))
}

func readChannelWriteSendResult(body []byte, offset int) (channelwrite.SendResult, int, error) {
	var result channelwrite.SendResult
	var reason byte
	var err error
	if result.MessageID, offset, err = readUvarint(body, offset); err != nil {
		return channelwrite.SendResult{}, offset, err
	}
	if result.MessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return channelwrite.SendResult{}, offset, err
	}
	if reason, offset, err = readByte(body, offset, "channel write result reason"); err != nil {
		return channelwrite.SendResult{}, offset, err
	}
	result.Reason = channelwrite.Reason(reason)
	return result, offset, nil
}

func appendChannelWriteResultError(dst []byte, err error) []byte {
	code := channelWriteErrorCode(err)
	dst = appendString(dst, code)
	if err == nil {
		return appendString(dst, "")
	}
	return appendString(dst, err.Error())
}

func readChannelWriteResultError(body []byte, offset int) (error, int, error) {
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
	case rpcStatusOK:
		return nil, offset, nil
	case channelWriteErrCodeNotLeader:
		return channelwrite.ErrNotLeader, offset, nil
	case channelWriteErrCodeNotChannelAuthority:
		return channelwrite.ErrNotChannelAuthority, offset, nil
	case channelWriteErrCodeStaleRoute:
		return channelwrite.ErrStaleRoute, offset, nil
	case channelWriteErrCodeRouteNotReady:
		return channelwrite.ErrRouteNotReady, offset, nil
	case channelWriteErrCodeBackpressured:
		return channelwrite.ErrBackpressured, offset, nil
	case channelWriteErrCodeAppendResultMissing:
		return channelwrite.ErrAppendResultMissing, offset, nil
	case channelWriteErrCodeChannelBusy:
		return channelwrite.ErrChannelBusy, offset, nil
	case channelWriteErrCodeContextCanceled:
		return context.Canceled, offset, nil
	case channelWriteErrCodeContextDeadlineExceeded:
		return context.DeadlineExceeded, offset, nil
	case channelWriteErrCodeRejected:
		if msg == "" {
			msg = "channel write result rejected"
		}
		return fmt.Errorf("internalv2/access/node: %s", msg), offset, nil
	default:
		return nil, offset, fmt.Errorf("internalv2/access/node: unknown channel write result error code %q", code)
	}
}

func channelWriteErrorCode(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, channelwrite.ErrNotLeader):
		return channelWriteErrCodeNotLeader
	case errors.Is(err, channelwrite.ErrNotChannelAuthority):
		return channelWriteErrCodeNotChannelAuthority
	case errors.Is(err, channelwrite.ErrStaleRoute):
		return channelWriteErrCodeStaleRoute
	case errors.Is(err, channelwrite.ErrRouteNotReady):
		return channelWriteErrCodeRouteNotReady
	case errors.Is(err, channelwrite.ErrBackpressured):
		return channelWriteErrCodeBackpressured
	case errors.Is(err, channelwrite.ErrAppendResultMissing):
		return channelWriteErrCodeAppendResultMissing
	case errors.Is(err, channelwrite.ErrChannelBusy):
		return channelWriteErrCodeChannelBusy
	case errors.Is(err, context.Canceled):
		return channelWriteErrCodeContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return channelWriteErrCodeContextDeadlineExceeded
	default:
		return channelWriteErrCodeRejected
	}
}

func appendChannelWriteStringSlice(dst []byte, values []string) []byte {
	dst = appendUvarint(dst, uint64(len(values)))
	for _, value := range values {
		dst = appendString(dst, value)
	}
	return dst
}

func readChannelWriteStringSlice(body []byte, offset int, label string) ([]string, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, nil
	}
	if err := validateChannelWriteCollectionLen(count, len(body)-offset, label); err != nil {
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

func appendChannelWriteBool(dst []byte, value bool) []byte {
	if value {
		return append(dst, 1)
	}
	return append(dst, 0)
}

func readChannelWriteBool(body []byte, offset int, label string) (bool, int, error) {
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

func validateChannelWriteCollectionLen(count uint64, remaining int, label string) error {
	if count > maxChannelWriteCollectionLen {
		return fmt.Errorf("internalv2/access/node: %s length exceeds limit", label)
	}
	if count > uint64(remaining) {
		return fmt.Errorf("internalv2/access/node: %s length exceeds payload", label)
	}
	return nil
}
