package node

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/legacy/observability/diagnostics"
)

const (
	maxDiagnosticsEvents      = 500
	maxDiagnosticsNotes       = 64
	maxDiagnosticsStringBytes = 4096
	maxDiagnosticsBodyBytes   = 4 << 20
)

var (
	diagnosticsRequestMagic    = [...]byte{'W', 'K', 'D', 'Q', 2}
	diagnosticsResponseMagicV2 = [...]byte{'W', 'K', 'D', 'R', 2}
	diagnosticsResponseMagic   = [...]byte{'W', 'K', 'D', 'R', 3}
)

// encodeDiagnosticsRequestBinary encodes diagnostics requests without JSON fallback.
func encodeDiagnosticsRequestBinary(req diagnosticsRequest) ([]byte, error) {
	dst := make([]byte, 0, len(diagnosticsRequestMagic)+128)
	dst = append(dst, diagnosticsRequestMagic[:]...)
	dst = appendDiagnosticsQuery(dst, req.Query)
	return dst, nil
}

func decodeDiagnosticsRequest(body []byte) (diagnosticsRequest, error) {
	if len(body) > maxDiagnosticsBodyBytes {
		return diagnosticsRequest{}, fmt.Errorf("access/node: diagnostics request body too large")
	}
	if !isDiagnosticsRequestBinary(body) {
		return diagnosticsRequest{}, fmt.Errorf("access/node: invalid diagnostics request codec")
	}
	offset := len(diagnosticsRequestMagic)
	query, next, err := readDiagnosticsQuery(body, offset)
	if err != nil {
		return diagnosticsRequest{}, err
	}
	if next != len(body) {
		return diagnosticsRequest{}, fmt.Errorf("access/node: trailing diagnostics request bytes")
	}
	return diagnosticsRequest{Query: query}, nil
}

func encodeDiagnosticsResponse(resp diagnosticsResponse) ([]byte, error) {
	dst := make([]byte, 0, len(diagnosticsResponseMagic)+256+estimateDiagnosticsEventsBinarySize(resp.Result.Events))
	dst = append(dst, diagnosticsResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendDiagnosticsQueryResult(dst, resp.Result)
	return dst, nil
}

func decodeDiagnosticsResponse(body []byte) (diagnosticsResponse, error) {
	if len(body) > maxDiagnosticsBodyBytes {
		return diagnosticsResponse{}, fmt.Errorf("access/node: diagnostics response body too large")
	}
	if !isDiagnosticsResponseBinary(body) {
		return diagnosticsResponse{}, fmt.Errorf("access/node: invalid diagnostics response codec")
	}
	version := diagnosticsResponseCodecVersion(body)
	offset := len(diagnosticsResponseMagic)
	var resp diagnosticsResponse
	var err error
	if resp.Status, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnosticsResponse{}, err
	}
	if resp.Result, offset, err = readDiagnosticsQueryResult(body, offset, version); err != nil {
		return diagnosticsResponse{}, err
	}
	if offset != len(body) {
		return diagnosticsResponse{}, fmt.Errorf("access/node: trailing diagnostics response bytes")
	}
	return resp, nil
}

func isDiagnosticsRequestBinary(body []byte) bool {
	return hasMagic(body, diagnosticsRequestMagic[:])
}

func isDiagnosticsResponseBinary(body []byte) bool {
	return diagnosticsResponseCodecVersion(body) != 0
}

func diagnosticsResponseCodecVersion(body []byte) int {
	switch {
	case hasMagic(body, diagnosticsResponseMagic[:]):
		return 3
	case hasMagic(body, diagnosticsResponseMagicV2[:]):
		return 2
	default:
		return 0
	}
}

func appendDiagnosticsQuery(dst []byte, query diagnostics.Query) []byte {
	dst = appendString(dst, query.TraceID)
	dst = appendString(dst, query.ClientMsgNo)
	dst = appendString(dst, query.ChannelKey)
	dst = appendString(dst, query.UID)
	dst = appendUvarint(dst, query.MessageSeq)
	dst = appendString(dst, string(query.Stage))
	dst = appendString(dst, string(query.Result))
	dst = appendNodeInt(dst, query.Limit)
	return dst
}

func readDiagnosticsQuery(body []byte, offset int) (diagnostics.Query, int, error) {
	var query diagnostics.Query
	var stage string
	var result string
	var err error
	if query.TraceID, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.Query{}, offset, err
	}
	if query.ClientMsgNo, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.Query{}, offset, err
	}
	if query.ChannelKey, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.Query{}, offset, err
	}
	if query.UID, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.Query{}, offset, err
	}
	if query.MessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return diagnostics.Query{}, offset, err
	}
	if stage, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.Query{}, offset, err
	}
	if result, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.Query{}, offset, err
	}
	if query.Limit, offset, err = readNodeInt(body, offset, "diagnostics query limit"); err != nil {
		return diagnostics.Query{}, offset, err
	}
	query.Stage = diagnostics.Stage(stage)
	query.Result = diagnostics.Result(result)
	return query, offset, nil
}

func appendDiagnosticsQueryResult(dst []byte, result diagnostics.QueryResult) []byte {
	dst = appendString(dst, result.Scope)
	dst = appendUvarint(dst, result.NodeID)
	dst = appendString(dst, result.TraceID)
	dst = appendString(dst, result.ClientMsgNo)
	dst = appendString(dst, result.ChannelKey)
	dst = appendString(dst, result.UID)
	dst = appendUvarint(dst, result.MessageSeq)
	dst = appendDiagnosticsQuery(dst, result.Query)
	dst = appendString(dst, string(result.Status))
	dst = appendDiagnosticsTime(dst, result.StartedAt)
	dst = appendNodeVarint(dst, result.DurationMS)
	dst = appendDiagnosticsSummary(dst, result.Summary)
	dst = appendDiagnosticsEvents(dst, result.Events)
	dst = appendDiagnosticsStringSlice(dst, result.Notes)
	return dst
}

func readDiagnosticsQueryResult(body []byte, offset int, eventCodecVersion int) (diagnostics.QueryResult, int, error) {
	var result diagnostics.QueryResult
	var status string
	var err error
	if result.Scope, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.QueryResult{}, offset, err
	}
	if result.NodeID, offset, err = readUvarint(body, offset); err != nil {
		return diagnostics.QueryResult{}, offset, err
	}
	if result.TraceID, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.QueryResult{}, offset, err
	}
	if result.ClientMsgNo, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.QueryResult{}, offset, err
	}
	if result.ChannelKey, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.QueryResult{}, offset, err
	}
	if result.UID, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.QueryResult{}, offset, err
	}
	if result.MessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return diagnostics.QueryResult{}, offset, err
	}
	if result.Query, offset, err = readDiagnosticsQuery(body, offset); err != nil {
		return diagnostics.QueryResult{}, offset, err
	}
	if status, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.QueryResult{}, offset, err
	}
	if result.StartedAt, offset, err = readDiagnosticsTime(body, offset); err != nil {
		return diagnostics.QueryResult{}, offset, err
	}
	if result.DurationMS, offset, err = readNodeVarint(body, offset); err != nil {
		return diagnostics.QueryResult{}, offset, err
	}
	if result.Summary, offset, err = readDiagnosticsSummary(body, offset); err != nil {
		return diagnostics.QueryResult{}, offset, err
	}
	if result.Events, offset, err = readDiagnosticsEvents(body, offset, eventCodecVersion); err != nil {
		return diagnostics.QueryResult{}, offset, err
	}
	if result.Notes, offset, err = readDiagnosticsStringSlice(body, offset, "diagnostics notes"); err != nil {
		return diagnostics.QueryResult{}, offset, err
	}
	result.Status = diagnostics.Status(status)
	return result, offset, nil
}

func appendDiagnosticsSummary(dst []byte, summary diagnostics.QuerySummary) []byte {
	dst = appendString(dst, summary.SlowestStage)
	dst = appendNodeVarint(dst, summary.SlowestDurationMS)
	dst = appendString(dst, summary.ErrorStage)
	dst = appendString(dst, summary.ErrorCode)
	return dst
}

func readDiagnosticsSummary(body []byte, offset int) (diagnostics.QuerySummary, int, error) {
	var summary diagnostics.QuerySummary
	var err error
	if summary.SlowestStage, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.QuerySummary{}, offset, err
	}
	if summary.SlowestDurationMS, offset, err = readNodeVarint(body, offset); err != nil {
		return diagnostics.QuerySummary{}, offset, err
	}
	if summary.ErrorStage, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.QuerySummary{}, offset, err
	}
	if summary.ErrorCode, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.QuerySummary{}, offset, err
	}
	return summary, offset, nil
}

func appendDiagnosticsEvents(dst []byte, events []diagnostics.Event) []byte {
	if events == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = appendUvarint(dst, uint64(len(events)))
	for _, event := range events {
		dst = appendDiagnosticsEvent(dst, event)
	}
	return dst
}

func readDiagnosticsEvents(body []byte, offset int, eventCodecVersion int) ([]diagnostics.Event, int, error) {
	marker, next, err := readNodeMarker(body, offset, "diagnostics events")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	count, next, err := readUvarint(body, next)
	if err != nil {
		return nil, offset, err
	}
	if count > maxDiagnosticsEvents {
		return nil, offset, fmt.Errorf("access/node: diagnostics events exceeds limit")
	}
	offset = next
	eventsLen, err := readCollectionLen(count, len(body)-offset, "diagnostics events")
	if err != nil {
		return nil, offset, err
	}
	events := make([]diagnostics.Event, eventsLen)
	for i := range events {
		if events[i], offset, err = readDiagnosticsEvent(body, offset, eventCodecVersion); err != nil {
			return nil, offset, err
		}
	}
	return events, offset, nil
}

func appendDiagnosticsEvent(dst []byte, event diagnostics.Event) []byte {
	dst = appendString(dst, event.TraceID)
	dst = appendString(dst, event.SpanID)
	dst = appendString(dst, event.ParentSpanID)
	dst = appendString(dst, string(event.Stage))
	dst = appendDiagnosticsTime(dst, event.At)
	dst = appendNodeVarint(dst, int64(event.Duration))
	dst = appendUvarint(dst, event.NodeID)
	dst = appendUvarint(dst, event.PeerNodeID)
	dst = appendUvarint(dst, uint64(event.SlotID))
	dst = appendString(dst, event.ChannelKey)
	dst = appendString(dst, event.ClientMsgNo)
	dst = appendUvarint(dst, event.MessageSeq)
	dst = appendUvarint(dst, event.RangeStart)
	dst = appendUvarint(dst, event.RangeEnd)
	dst = appendString(dst, event.Service)
	dst = appendString(dst, string(event.Result))
	dst = appendString(dst, string(event.ErrorCode))
	dst = appendString(dst, event.Error)
	dst = appendNodeInt(dst, event.Attempt)
	dst = appendNodeInt(dst, event.RequestCount)
	dst = appendNodeInt(dst, event.RecordCount)
	dst = appendNodeInt(dst, event.ByteCount)
	dst = appendNodeInt(dst, event.QueueDepth)
	dst = appendString(dst, event.ReplicaRole)
	dst = appendString(dst, event.SampleReason)
	return dst
}

func readDiagnosticsEvent(body []byte, offset int, eventCodecVersion int) (diagnostics.Event, int, error) {
	var event diagnostics.Event
	var stage string
	var result string
	var errorCode string
	var slotID uint64
	var err error
	if event.TraceID, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.Event{}, offset, err
	}
	if event.SpanID, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.Event{}, offset, err
	}
	if event.ParentSpanID, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.Event{}, offset, err
	}
	if stage, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.Event{}, offset, err
	}
	if event.At, offset, err = readDiagnosticsTime(body, offset); err != nil {
		return diagnostics.Event{}, offset, err
	}
	var duration int64
	if duration, offset, err = readNodeVarint(body, offset); err != nil {
		return diagnostics.Event{}, offset, err
	}
	if event.NodeID, offset, err = readUvarint(body, offset); err != nil {
		return diagnostics.Event{}, offset, err
	}
	if event.PeerNodeID, offset, err = readUvarint(body, offset); err != nil {
		return diagnostics.Event{}, offset, err
	}
	if slotID, offset, err = readUvarint(body, offset); err != nil {
		return diagnostics.Event{}, offset, err
	}
	if slotID > uint64(^uint32(0)) {
		return diagnostics.Event{}, offset, fmt.Errorf("access/node: diagnostics slot id overflows uint32")
	}
	if event.ChannelKey, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.Event{}, offset, err
	}
	if event.ClientMsgNo, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.Event{}, offset, err
	}
	if event.MessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return diagnostics.Event{}, offset, err
	}
	if event.RangeStart, offset, err = readUvarint(body, offset); err != nil {
		return diagnostics.Event{}, offset, err
	}
	if event.RangeEnd, offset, err = readUvarint(body, offset); err != nil {
		return diagnostics.Event{}, offset, err
	}
	if event.Service, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.Event{}, offset, err
	}
	if result, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.Event{}, offset, err
	}
	if errorCode, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.Event{}, offset, err
	}
	if event.Error, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.Event{}, offset, err
	}
	if event.Attempt, offset, err = readNodeInt(body, offset, "diagnostics attempt"); err != nil {
		return diagnostics.Event{}, offset, err
	}
	if eventCodecVersion >= 3 {
		if event.RequestCount, offset, err = readNodeInt(body, offset, "diagnostics request count"); err != nil {
			return diagnostics.Event{}, offset, err
		}
		if event.RecordCount, offset, err = readNodeInt(body, offset, "diagnostics record count"); err != nil {
			return diagnostics.Event{}, offset, err
		}
		if event.ByteCount, offset, err = readNodeInt(body, offset, "diagnostics byte count"); err != nil {
			return diagnostics.Event{}, offset, err
		}
	}
	if event.QueueDepth, offset, err = readNodeInt(body, offset, "diagnostics queue depth"); err != nil {
		return diagnostics.Event{}, offset, err
	}
	if event.ReplicaRole, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.Event{}, offset, err
	}
	if event.SampleReason, offset, err = readDiagnosticsString(body, offset); err != nil {
		return diagnostics.Event{}, offset, err
	}
	event.Stage = diagnostics.Stage(stage)
	event.Duration = time.Duration(duration)
	event.SlotID = uint32(slotID)
	event.Result = diagnostics.Result(result)
	event.ErrorCode = diagnostics.ErrorCode(errorCode)
	return event, offset, nil
}

func appendDiagnosticsStringSlice(dst []byte, values []string) []byte {
	if values == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = appendUvarint(dst, uint64(len(values)))
	for _, value := range values {
		dst = appendString(dst, value)
	}
	return dst
}

func readDiagnosticsStringSlice(body []byte, offset int, label string) ([]string, int, error) {
	marker, next, err := readNodeMarker(body, offset, label)
	if err != nil || marker == 0 {
		return nil, next, err
	}
	count, next, err := readUvarint(body, next)
	if err != nil {
		return nil, offset, err
	}
	if count > maxDiagnosticsNotes {
		return nil, offset, fmt.Errorf("access/node: %s exceeds limit", label)
	}
	offset = next
	valuesLen, err := readCollectionLen(count, len(body)-offset, label)
	if err != nil {
		return nil, offset, err
	}
	values := make([]string, valuesLen)
	for i := range values {
		if values[i], offset, err = readDiagnosticsString(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return values, offset, nil
}

func readDiagnosticsString(body []byte, offset int) (string, int, error) {
	length, next, err := readUvarint(body, offset)
	if err != nil {
		return "", offset, err
	}
	if length > maxDiagnosticsStringBytes {
		return "", offset, fmt.Errorf("access/node: diagnostics string exceeds limit")
	}
	offset = next
	end := offset + int(length)
	if end < offset || end > len(body) {
		return "", offset, fmt.Errorf("access/node: short diagnostics string")
	}
	return string(body[offset:end]), end, nil
}

func appendDiagnosticsTime(dst []byte, value time.Time) []byte {
	if value.IsZero() {
		return appendNodeVarint(dst, 0)
	}
	return appendNodeVarint(dst, value.UnixNano())
}

func readDiagnosticsTime(body []byte, offset int) (time.Time, int, error) {
	ns, next, err := readNodeVarint(body, offset)
	if err != nil || ns == 0 {
		return time.Time{}, next, err
	}
	return time.Unix(0, ns).UTC(), next, nil
}

func estimateDiagnosticsEventsBinarySize(events []diagnostics.Event) int {
	size := 1 + binaryMaxVarintLen64()
	for _, event := range events {
		size += len(event.TraceID) + len(event.SpanID) + len(event.ParentSpanID) + len(event.Stage)
		size += len(event.ChannelKey) + len(event.ClientMsgNo) + len(event.Service) + len(event.Result)
		size += len(event.ErrorCode) + len(event.Error) + len(event.ReplicaRole) + len(event.SampleReason) + 160
	}
	return size
}
