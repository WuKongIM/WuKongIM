package node

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
)

var (
	channelAppendRequestMagic  = [...]byte{'W', 'K', 'V', 'A', 2}
	channelAppendResponseMagic = [...]byte{'W', 'K', 'V', 'a', 1}
)

const maxChannelAppendCollectionLen = 4096

const (
	channelAppendErrCodeNotLeader               = "not_leader"
	channelAppendErrCodeStaleRoute              = "stale_route"
	channelAppendErrCodeRouteNotReady           = "route_not_ready"
	channelAppendErrCodeChannelBusy             = "channel_busy"
	channelAppendErrCodeContextCanceled         = "context_canceled"
	channelAppendErrCodeContextDeadlineExceeded = "context_deadline_exceeded"
	channelAppendErrCodeRejected                = "rejected"
	channelAppendErrCodeNotChannelAuthority     = "not_channel_authority"
	channelAppendErrCodeBackpressured           = "backpressured"
	channelAppendErrCodeAppendResultMissing     = "append_result_missing"
)

// channelAppendItem is one send command plus the relative timeout transported over RPC.
type channelAppendItem struct {
	// Command is the entry-agnostic SEND command.
	Command channelappend.SendCommand
	// Timeout is the relative item deadline encoded for the authority node.
	Timeout time.Duration
}

// channelAppendRequest is the deterministic binary DTO for channel append calls.
type channelAppendRequest struct {
	// Target fences the request to one observed channel authority.
	Target channelappend.AuthorityTarget
	// Items carries item-aligned send commands for the channel authority.
	Items []channelAppendItem
}

// channelAppendResponse is the deterministic binary DTO returned by channel append calls.
type channelAppendResponse struct {
	// Status is the envelope-level RPC status.
	Status string
	// Results carries item-aligned SEND outcomes when Status is ok.
	Results []channelappend.SendBatchItemResult
}

func encodeChannelAppendRequest(req channelAppendRequest) ([]byte, error) {
	dst := make([]byte, 0, 128)
	dst = append(dst, channelAppendRequestMagic[:]...)
	dst = appendChannelAppendTarget(dst, req.Target)
	dst = appendChannelAppendItems(dst, req.Items)
	return dst, nil
}

func decodeChannelAppendRequest(body []byte) (channelAppendRequest, error) {
	if !hasMagic(body, channelAppendRequestMagic[:]) {
		return channelAppendRequest{}, fmt.Errorf("internalv2/access/node: invalid channel append request codec")
	}
	offset := len(channelAppendRequestMagic)
	var req channelAppendRequest
	var err error
	if req.Target, offset, err = readChannelAppendTarget(body, offset); err != nil {
		return channelAppendRequest{}, err
	}
	if req.Items, offset, err = readChannelAppendItems(body, offset); err != nil {
		return channelAppendRequest{}, err
	}
	if offset != len(body) {
		return channelAppendRequest{}, fmt.Errorf("internalv2/access/node: trailing channel append request bytes")
	}
	return req, nil
}

func encodeChannelAppendResponse(resp channelAppendResponse) ([]byte, error) {
	dst := make([]byte, 0, 128)
	dst = append(dst, channelAppendResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendChannelAppendResults(dst, resp.Results)
	return dst, nil
}

func decodeChannelAppendResponse(body []byte) (channelAppendResponse, error) {
	if !hasMagic(body, channelAppendResponseMagic[:]) {
		return channelAppendResponse{}, fmt.Errorf("internalv2/access/node: invalid channel append response codec")
	}
	offset := len(channelAppendResponseMagic)
	var resp channelAppendResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return channelAppendResponse{}, err
	}
	if resp.Results, offset, err = readChannelAppendResults(body, offset); err != nil {
		return channelAppendResponse{}, err
	}
	if offset != len(body) {
		return channelAppendResponse{}, fmt.Errorf("internalv2/access/node: trailing channel append response bytes")
	}
	return resp, nil
}

func appendChannelAppendTarget(dst []byte, target channelappend.AuthorityTarget) []byte {
	dst = appendString(dst, target.ChannelID.ID)
	dst = append(dst, target.ChannelID.Type)
	dst = appendString(dst, target.ChannelKey)
	dst = appendUvarint(dst, target.LeaderNodeID)
	dst = appendUvarint(dst, target.Epoch)
	dst = appendUvarint(dst, target.LeaderEpoch)
	dst = appendChannelAppendBool(dst, target.Large)
	return appendUvarint(dst, target.SubscriberMutationVersion)
}

func readChannelAppendTarget(body []byte, offset int) (channelappend.AuthorityTarget, int, error) {
	var target channelappend.AuthorityTarget
	var err error
	if target.ChannelID.ID, offset, err = readString(body, offset); err != nil {
		return channelappend.AuthorityTarget{}, offset, err
	}
	if target.ChannelID.Type, offset, err = readByte(body, offset, "channel append target channel type"); err != nil {
		return channelappend.AuthorityTarget{}, offset, err
	}
	if target.ChannelKey, offset, err = readString(body, offset); err != nil {
		return channelappend.AuthorityTarget{}, offset, err
	}
	if target.LeaderNodeID, offset, err = readUvarint(body, offset); err != nil {
		return channelappend.AuthorityTarget{}, offset, err
	}
	if target.Epoch, offset, err = readUvarint(body, offset); err != nil {
		return channelappend.AuthorityTarget{}, offset, err
	}
	if target.LeaderEpoch, offset, err = readUvarint(body, offset); err != nil {
		return channelappend.AuthorityTarget{}, offset, err
	}
	if target.Large, offset, err = readChannelAppendBool(body, offset, "channel append target large"); err != nil {
		return channelappend.AuthorityTarget{}, offset, err
	}
	if target.SubscriberMutationVersion, offset, err = readUvarint(body, offset); err != nil {
		return channelappend.AuthorityTarget{}, offset, err
	}
	return target, offset, nil
}

func appendChannelAppendItems(dst []byte, items []channelAppendItem) []byte {
	dst = appendUvarint(dst, uint64(len(items)))
	for _, item := range items {
		dst = appendChannelAppendItem(dst, item)
	}
	return dst
}

func readChannelAppendItems(body []byte, offset int) ([]channelAppendItem, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, fmt.Errorf("internalv2/access/node: empty channel append request")
	}
	if err := validateChannelAppendCollectionLen(count, len(body)-offset, "channel append items"); err != nil {
		return nil, offset, err
	}
	items := make([]channelAppendItem, 0, int(count))
	for i := uint64(0); i < count; i++ {
		var item channelAppendItem
		if item, offset, err = readChannelAppendItem(body, offset); err != nil {
			return nil, offset, err
		}
		items = append(items, item)
	}
	return items, offset, nil
}

func appendChannelAppendItem(dst []byte, item channelAppendItem) []byte {
	dst = appendChannelAppendSendCommand(dst, item.Command)
	return appendVarint(dst, int64(item.Timeout))
}

func readChannelAppendItem(body []byte, offset int) (channelAppendItem, int, error) {
	var item channelAppendItem
	var timeout int64
	var err error
	if item.Command, offset, err = readChannelAppendSendCommand(body, offset); err != nil {
		return channelAppendItem{}, offset, err
	}
	if timeout, offset, err = readVarint(body, offset); err != nil {
		return channelAppendItem{}, offset, err
	}
	if timeout < 0 {
		return channelAppendItem{}, offset, fmt.Errorf("internalv2/access/node: channel append timeout is negative")
	}
	item.Timeout = time.Duration(timeout)
	return item, offset, nil
}

func appendChannelAppendSendCommand(dst []byte, cmd channelappend.SendCommand) []byte {
	dst = appendString(dst, cmd.FromUID)
	dst = appendString(dst, cmd.DeviceID)
	dst = append(dst, cmd.DeviceFlag)
	dst = appendUvarint(dst, cmd.SenderNodeID)
	dst = appendUvarint(dst, cmd.SenderSessionID)
	dst = appendUvarint(dst, cmd.ClientSeq)
	dst = appendString(dst, cmd.ClientMsgNo)
	dst = appendString(dst, cmd.TraceID)
	dst = appendString(dst, cmd.ChannelKey)
	dst = appendString(dst, cmd.ChannelID)
	dst = append(dst, cmd.ChannelType)
	dst = append(dst, cmd.Setting)
	dst = appendString(dst, cmd.Topic)
	dst = appendUvarint(dst, uint64(cmd.Expire))
	dst = appendBytes(dst, cmd.Payload)
	dst = appendChannelAppendBool(dst, cmd.NoPersist)
	dst = appendChannelAppendBool(dst, cmd.SyncOnce)
	dst = appendChannelAppendBool(dst, cmd.RedDot)
	dst = appendChannelAppendBool(dst, cmd.NormalizePersonChannel)
	dst = appendChannelAppendBool(dst, cmd.RequestScoped)
	dst = appendChannelAppendStringSlice(dst, cmd.MessageScopedUIDs)
	dst = appendUvarint(dst, cmd.MessageID)
	dst = append(dst, cmd.ProtocolVersion)
	dst = appendString(dst, string(cmd.Origin))
	dst = appendVarint(dst, int64(cmd.HookDepth))
	return appendChannelAppendBool(dst, cmd.SkipPluginHooks)
}

func readChannelAppendSendCommand(body []byte, offset int) (channelappend.SendCommand, int, error) {
	var cmd channelappend.SendCommand
	var err error
	if cmd.FromUID, offset, err = readString(body, offset); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if cmd.DeviceID, offset, err = readString(body, offset); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if cmd.DeviceFlag, offset, err = readByte(body, offset, "channel append command device flag"); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if cmd.SenderNodeID, offset, err = readUvarint(body, offset); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if cmd.SenderSessionID, offset, err = readUvarint(body, offset); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if cmd.ClientSeq, offset, err = readUvarint(body, offset); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if cmd.ClientMsgNo, offset, err = readString(body, offset); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if cmd.TraceID, offset, err = readString(body, offset); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if cmd.ChannelKey, offset, err = readString(body, offset); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if cmd.ChannelID, offset, err = readString(body, offset); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if cmd.ChannelType, offset, err = readByte(body, offset, "channel append command channel type"); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if cmd.Setting, offset, err = readByte(body, offset, "channel append command setting"); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if cmd.Topic, offset, err = readString(body, offset); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	expire, next, err := readUvarint(body, offset)
	if err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if expire > uint64(^uint32(0)) {
		return channelappend.SendCommand{}, offset, fmt.Errorf("internalv2/access/node: channel append expire overflows uint32")
	}
	cmd.Expire = uint32(expire)
	offset = next
	if cmd.Payload, offset, err = readBytes(body, offset); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if cmd.NoPersist, offset, err = readChannelAppendBool(body, offset, "channel append no persist"); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if cmd.SyncOnce, offset, err = readChannelAppendBool(body, offset, "channel append sync once"); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if cmd.RedDot, offset, err = readChannelAppendBool(body, offset, "channel append red dot"); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if cmd.NormalizePersonChannel, offset, err = readChannelAppendBool(body, offset, "channel append normalize person channel"); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if cmd.RequestScoped, offset, err = readChannelAppendBool(body, offset, "channel append request scoped"); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if cmd.MessageScopedUIDs, offset, err = readChannelAppendStringSlice(body, offset, "channel append message scoped uids"); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if cmd.MessageID, offset, err = readUvarint(body, offset); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if cmd.ProtocolVersion, offset, err = readByte(body, offset, "channel append protocol version"); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	var origin string
	if origin, offset, err = readString(body, offset); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	cmd.Origin = channelappend.SendOrigin(origin)
	var hookDepth int64
	if hookDepth, offset, err = readVarint(body, offset); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	if hookDepth < 0 {
		return channelappend.SendCommand{}, offset, fmt.Errorf("internalv2/access/node: channel append hook depth is negative")
	}
	cmd.HookDepth = int(hookDepth)
	if cmd.SkipPluginHooks, offset, err = readChannelAppendBool(body, offset, "channel append skip plugin hooks"); err != nil {
		return channelappend.SendCommand{}, offset, err
	}
	return cmd, offset, nil
}

func appendChannelAppendResults(dst []byte, results []channelappend.SendBatchItemResult) []byte {
	dst = appendUvarint(dst, uint64(len(results)))
	for _, result := range results {
		dst = appendChannelAppendResult(dst, result)
	}
	return dst
}

func readChannelAppendResults(body []byte, offset int) ([]channelappend.SendBatchItemResult, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, nil
	}
	if err := validateChannelAppendCollectionLen(count, len(body)-offset, "channel append results"); err != nil {
		return nil, offset, err
	}
	results := make([]channelappend.SendBatchItemResult, 0, int(count))
	for i := uint64(0); i < count; i++ {
		var result channelappend.SendBatchItemResult
		if result, offset, err = readChannelAppendResult(body, offset); err != nil {
			return nil, offset, err
		}
		results = append(results, result)
	}
	return results, offset, nil
}

func appendChannelAppendResult(dst []byte, result channelappend.SendBatchItemResult) []byte {
	dst = appendChannelAppendSendResult(dst, result.Result)
	return appendChannelAppendResultError(dst, result.Err)
}

func readChannelAppendResult(body []byte, offset int) (channelappend.SendBatchItemResult, int, error) {
	var result channelappend.SendBatchItemResult
	var err error
	if result.Result, offset, err = readChannelAppendSendResult(body, offset); err != nil {
		return channelappend.SendBatchItemResult{}, offset, err
	}
	if result.Err, offset, err = readChannelAppendResultError(body, offset); err != nil {
		return channelappend.SendBatchItemResult{}, offset, err
	}
	return result, offset, nil
}

func appendChannelAppendSendResult(dst []byte, result channelappend.SendResult) []byte {
	dst = appendUvarint(dst, result.MessageID)
	dst = appendUvarint(dst, result.MessageSeq)
	return append(dst, byte(result.Reason))
}

func readChannelAppendSendResult(body []byte, offset int) (channelappend.SendResult, int, error) {
	var result channelappend.SendResult
	var reason byte
	var err error
	if result.MessageID, offset, err = readUvarint(body, offset); err != nil {
		return channelappend.SendResult{}, offset, err
	}
	if result.MessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return channelappend.SendResult{}, offset, err
	}
	if reason, offset, err = readByte(body, offset, "channel append result reason"); err != nil {
		return channelappend.SendResult{}, offset, err
	}
	result.Reason = channelappend.Reason(reason)
	return result, offset, nil
}

func appendChannelAppendResultError(dst []byte, err error) []byte {
	code := channelAppendErrorCode(err)
	dst = appendString(dst, code)
	if err == nil {
		return appendString(dst, "")
	}
	return appendString(dst, err.Error())
}

func readChannelAppendResultError(body []byte, offset int) (error, int, error) {
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
	case channelAppendErrCodeNotLeader:
		return channelappend.ErrNotLeader, offset, nil
	case channelAppendErrCodeNotChannelAuthority:
		return channelappend.ErrNotChannelAuthority, offset, nil
	case channelAppendErrCodeStaleRoute:
		return channelappend.ErrStaleRoute, offset, nil
	case channelAppendErrCodeRouteNotReady:
		return channelappend.ErrRouteNotReady, offset, nil
	case channelAppendErrCodeBackpressured:
		return channelappend.ErrBackpressured, offset, nil
	case channelAppendErrCodeAppendResultMissing:
		return channelappend.ErrAppendResultMissing, offset, nil
	case channelAppendErrCodeChannelBusy:
		return channelappend.ErrChannelBusy, offset, nil
	case channelAppendErrCodeContextCanceled:
		return context.Canceled, offset, nil
	case channelAppendErrCodeContextDeadlineExceeded:
		return context.DeadlineExceeded, offset, nil
	case channelAppendErrCodeRejected:
		if msg == "" {
			msg = "channel append result rejected"
		}
		return fmt.Errorf("internalv2/access/node: %s", msg), offset, nil
	default:
		return nil, offset, fmt.Errorf("internalv2/access/node: unknown channel append result error code %q", code)
	}
}

func channelAppendErrorCode(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, channelappend.ErrNotLeader):
		return channelAppendErrCodeNotLeader
	case errors.Is(err, channelappend.ErrNotChannelAuthority):
		return channelAppendErrCodeNotChannelAuthority
	case errors.Is(err, channelappend.ErrStaleRoute):
		return channelAppendErrCodeStaleRoute
	case errors.Is(err, channelappend.ErrRouteNotReady):
		return channelAppendErrCodeRouteNotReady
	case errors.Is(err, channelappend.ErrBackpressured):
		return channelAppendErrCodeBackpressured
	case errors.Is(err, channelappend.ErrAppendResultMissing):
		return channelAppendErrCodeAppendResultMissing
	case errors.Is(err, channelappend.ErrChannelBusy):
		return channelAppendErrCodeChannelBusy
	case errors.Is(err, context.Canceled):
		return channelAppendErrCodeContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return channelAppendErrCodeContextDeadlineExceeded
	default:
		return channelAppendErrCodeRejected
	}
}

func appendChannelAppendStringSlice(dst []byte, values []string) []byte {
	dst = appendUvarint(dst, uint64(len(values)))
	for _, value := range values {
		dst = appendString(dst, value)
	}
	return dst
}

func readChannelAppendStringSlice(body []byte, offset int, label string) ([]string, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, nil
	}
	if err := validateChannelAppendCollectionLen(count, len(body)-offset, label); err != nil {
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

func appendChannelAppendBool(dst []byte, value bool) []byte {
	if value {
		return append(dst, 1)
	}
	return append(dst, 0)
}

func readChannelAppendBool(body []byte, offset int, label string) (bool, int, error) {
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

func validateChannelAppendCollectionLen(count uint64, remaining int, label string) error {
	if count > maxChannelAppendCollectionLen {
		return fmt.Errorf("internalv2/access/node: %s length exceeds limit", label)
	}
	if count > uint64(remaining) {
		return fmt.Errorf("internalv2/access/node: %s length exceeds payload", label)
	}
	return nil
}
