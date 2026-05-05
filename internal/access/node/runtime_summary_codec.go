package node

import (
	"fmt"
	"sort"
)

var (
	runtimeSummaryRequestMagic  = [...]byte{'W', 'K', 'R', 'S', 1}
	runtimeSummaryResponseMagic = [...]byte{'W', 'K', 'R', 'T', 1}
)

// encodeRuntimeSummaryRequestBinary encodes runtime summary requests without JSON reflection.
func encodeRuntimeSummaryRequestBinary(req runtimeSummaryRequest) ([]byte, error) {
	dst := make([]byte, 0, len(runtimeSummaryRequestMagic)+10)
	dst = append(dst, runtimeSummaryRequestMagic[:]...)
	dst = appendUvarint(dst, req.NodeID)
	return dst, nil
}

func decodeRuntimeSummaryRequest(body []byte) (runtimeSummaryRequest, error) {
	if !isRuntimeSummaryRequestBinary(body) {
		return runtimeSummaryRequest{}, fmt.Errorf("access/node: invalid runtime summary request codec")
	}
	offset := len(runtimeSummaryRequestMagic)
	nodeID, next, err := readUvarint(body, offset)
	if err != nil {
		return runtimeSummaryRequest{}, err
	}
	if next != len(body) {
		return runtimeSummaryRequest{}, fmt.Errorf("access/node: trailing runtime summary request bytes")
	}
	return runtimeSummaryRequest{NodeID: nodeID}, nil
}

func encodeRuntimeSummaryResponseBinary(resp runtimeSummaryResponse) ([]byte, error) {
	dst := make([]byte, 0, len(runtimeSummaryResponseMagic)+64+len(resp.Summary.SessionsByListener)*24)
	dst = append(dst, runtimeSummaryResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendRuntimeSummary(dst, resp.Summary)
	return dst, nil
}

func decodeRuntimeSummaryResponseBinary(body []byte) (runtimeSummaryResponse, error) {
	if !isRuntimeSummaryResponseBinary(body) {
		return runtimeSummaryResponse{}, fmt.Errorf("access/node: invalid runtime summary response codec")
	}
	offset := len(runtimeSummaryResponseMagic)
	var resp runtimeSummaryResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return runtimeSummaryResponse{}, err
	}
	if resp.Summary, offset, err = readRuntimeSummary(body, offset); err != nil {
		return runtimeSummaryResponse{}, err
	}
	if offset != len(body) {
		return runtimeSummaryResponse{}, fmt.Errorf("access/node: trailing runtime summary response bytes")
	}
	return resp, nil
}

func isRuntimeSummaryRequestBinary(body []byte) bool {
	return hasMagic(body, runtimeSummaryRequestMagic[:])
}

func isRuntimeSummaryResponseBinary(body []byte) bool {
	return hasMagic(body, runtimeSummaryResponseMagic[:])
}

func appendRuntimeSummary(dst []byte, summary RuntimeSummary) []byte {
	dst = appendUvarint(dst, summary.NodeID)
	dst = appendRuntimeSummaryInt(dst, summary.ActiveOnline)
	dst = appendRuntimeSummaryInt(dst, summary.ClosingOnline)
	dst = appendRuntimeSummaryInt(dst, summary.TotalOnline)
	dst = appendRuntimeSummaryInt(dst, summary.GatewaySessions)
	dst = appendRuntimeSummaryListenerMap(dst, summary.SessionsByListener)
	dst = appendNodeBool(dst, summary.AcceptingNewSessions)
	dst = appendNodeBool(dst, summary.Draining)
	dst = appendNodeBool(dst, summary.Unknown)
	return dst
}

func readRuntimeSummary(body []byte, offset int) (RuntimeSummary, int, error) {
	var summary RuntimeSummary
	var err error
	if summary.NodeID, offset, err = readUvarint(body, offset); err != nil {
		return RuntimeSummary{}, offset, err
	}
	if summary.ActiveOnline, offset, err = readRuntimeSummaryInt(body, offset); err != nil {
		return RuntimeSummary{}, offset, err
	}
	if summary.ClosingOnline, offset, err = readRuntimeSummaryInt(body, offset); err != nil {
		return RuntimeSummary{}, offset, err
	}
	if summary.TotalOnline, offset, err = readRuntimeSummaryInt(body, offset); err != nil {
		return RuntimeSummary{}, offset, err
	}
	if summary.GatewaySessions, offset, err = readRuntimeSummaryInt(body, offset); err != nil {
		return RuntimeSummary{}, offset, err
	}
	if summary.SessionsByListener, offset, err = readRuntimeSummaryListenerMap(body, offset); err != nil {
		return RuntimeSummary{}, offset, err
	}
	if summary.AcceptingNewSessions, offset, err = readNodeBool(body, offset); err != nil {
		return RuntimeSummary{}, offset, err
	}
	if summary.Draining, offset, err = readNodeBool(body, offset); err != nil {
		return RuntimeSummary{}, offset, err
	}
	if summary.Unknown, offset, err = readNodeBool(body, offset); err != nil {
		return RuntimeSummary{}, offset, err
	}
	return summary, offset, nil
}

func appendRuntimeSummaryInt(dst []byte, value int) []byte {
	return appendNodeVarint(dst, int64(value))
}

func readRuntimeSummaryInt(body []byte, offset int) (int, int, error) {
	value, next, err := readNodeVarint(body, offset)
	if err != nil {
		return 0, offset, err
	}
	maxInt := int64(^uint(0) >> 1)
	minInt := -maxInt - 1
	if value < minInt || value > maxInt {
		return 0, offset, fmt.Errorf("access/node: runtime summary int overflows")
	}
	return int(value), next, nil
}

func appendRuntimeSummaryListenerMap(dst []byte, values map[string]int) []byte {
	if values == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = appendUvarint(dst, uint64(len(values)))
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		dst = appendString(dst, key)
		dst = appendRuntimeSummaryInt(dst, values[key])
	}
	return dst
}

func readRuntimeSummaryListenerMap(body []byte, offset int) (map[string]int, int, error) {
	marker, next, err := readNodeMarker(body, offset, "runtime summary listener map")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	count, next, err := readUvarint(body, next)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	listenerLen, err := readCollectionLen(count, len(body)-offset, "runtime summary listener map")
	if err != nil {
		return nil, offset, err
	}
	values := make(map[string]int, listenerLen)
	for range listenerLen {
		var key string
		var value int
		if key, offset, err = readString(body, offset); err != nil {
			return nil, offset, err
		}
		if value, offset, err = readRuntimeSummaryInt(body, offset); err != nil {
			return nil, offset, err
		}
		values[key] = value
	}
	return values, offset, nil
}
