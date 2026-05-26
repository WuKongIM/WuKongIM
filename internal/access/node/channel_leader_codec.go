package node

import (
	"encoding/binary"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	channelLeaderRepairRequestMagic    = [...]byte{'W', 'K', 'L', 'R', 1}
	channelLeaderRepairResponseMagic   = [...]byte{'W', 'K', 'L', 'S', 1}
	channelLeaderEvaluateRequestMagic  = [...]byte{'W', 'K', 'L', 'E', 1}
	channelLeaderEvaluateResponseMagic = [...]byte{'W', 'K', 'L', 'P', 1}
	channelLeaderTransferRequestMagic  = [...]byte{'W', 'K', 'L', 'T', 1}
	channelLeaderTransferResponseMagic = [...]byte{'W', 'K', 'L', 'U', 1}
)

// encodeChannelLeaderRepairRequestBinary encodes channel leader repair requests without JSON reflection.
func encodeChannelLeaderRepairRequestBinary(req ChannelLeaderRepairRequest) ([]byte, error) {
	dst := make([]byte, 0, len(channelLeaderRepairRequestMagic)+len(req.ChannelID.ID)+len(req.Reason)+32)
	dst = append(dst, channelLeaderRepairRequestMagic[:]...)
	dst = appendChannelID(dst, req.ChannelID)
	dst = appendUvarint(dst, req.ObservedChannelEpoch)
	dst = appendUvarint(dst, req.ObservedLeaderEpoch)
	dst = appendString(dst, req.Reason)
	return dst, nil
}

func decodeChannelLeaderRepairRequest(body []byte) (ChannelLeaderRepairRequest, error) {
	if !isChannelLeaderRepairRequestBinary(body) {
		return ChannelLeaderRepairRequest{}, fmt.Errorf("access/node: invalid channel leader repair request codec")
	}
	offset := len(channelLeaderRepairRequestMagic)
	var req ChannelLeaderRepairRequest
	var err error
	if req.ChannelID, offset, err = readChannelID(body, offset); err != nil {
		return ChannelLeaderRepairRequest{}, err
	}
	if req.ObservedChannelEpoch, offset, err = readUvarint(body, offset); err != nil {
		return ChannelLeaderRepairRequest{}, err
	}
	if req.ObservedLeaderEpoch, offset, err = readUvarint(body, offset); err != nil {
		return ChannelLeaderRepairRequest{}, err
	}
	if req.Reason, offset, err = readString(body, offset); err != nil {
		return ChannelLeaderRepairRequest{}, err
	}
	if offset != len(body) {
		return ChannelLeaderRepairRequest{}, fmt.Errorf("access/node: trailing channel leader repair request bytes")
	}
	return req, nil
}

func encodeChannelLeaderRepairResponseBinary(resp channelLeaderRepairResponse) ([]byte, error) {
	dst := make([]byte, 0, len(channelLeaderRepairResponseMagic)+128)
	dst = append(dst, channelLeaderRepairResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendUvarint(dst, resp.LeaderID)
	dst = appendChannelLeaderRepairResultPtr(dst, resp.Result)
	return dst, nil
}

func decodeChannelLeaderRepairResponseBinary(body []byte) (channelLeaderRepairResponse, error) {
	if !isChannelLeaderRepairResponseBinary(body) {
		return channelLeaderRepairResponse{}, fmt.Errorf("access/node: invalid channel leader repair response codec")
	}
	offset := len(channelLeaderRepairResponseMagic)
	var resp channelLeaderRepairResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return channelLeaderRepairResponse{}, err
	}
	if resp.LeaderID, offset, err = readUvarint(body, offset); err != nil {
		return channelLeaderRepairResponse{}, err
	}
	if resp.Result, offset, err = readChannelLeaderRepairResultPtr(body, offset); err != nil {
		return channelLeaderRepairResponse{}, err
	}
	if offset != len(body) {
		return channelLeaderRepairResponse{}, fmt.Errorf("access/node: trailing channel leader repair response bytes")
	}
	return resp, nil
}

// encodeChannelLeaderEvaluateRequestBinary encodes channel leader evaluate requests without JSON reflection.
func encodeChannelLeaderEvaluateRequestBinary(req ChannelLeaderEvaluateRequest) ([]byte, error) {
	dst := make([]byte, 0, len(channelLeaderEvaluateRequestMagic)+128)
	dst = append(dst, channelLeaderEvaluateRequestMagic[:]...)
	dst = appendChannelRuntimeMeta(dst, req.Meta)
	return dst, nil
}

func decodeChannelLeaderEvaluateRequest(body []byte) (ChannelLeaderEvaluateRequest, error) {
	if !isChannelLeaderEvaluateRequestBinary(body) {
		return ChannelLeaderEvaluateRequest{}, fmt.Errorf("access/node: invalid channel leader evaluate request codec")
	}
	offset := len(channelLeaderEvaluateRequestMagic)
	meta, next, err := readChannelRuntimeMeta(body, offset)
	if err != nil {
		return ChannelLeaderEvaluateRequest{}, err
	}
	if next != len(body) {
		return ChannelLeaderEvaluateRequest{}, fmt.Errorf("access/node: trailing channel leader evaluate request bytes")
	}
	return ChannelLeaderEvaluateRequest{Meta: meta}, nil
}

func encodeChannelLeaderEvaluateResponseBinary(resp channelLeaderEvaluateResponse) ([]byte, error) {
	dst := make([]byte, 0, len(channelLeaderEvaluateResponseMagic)+128)
	dst = append(dst, channelLeaderEvaluateResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendChannelLeaderPromotionReportPtr(dst, resp.Report)
	return dst, nil
}

func decodeChannelLeaderEvaluateResponseBinary(body []byte) (channelLeaderEvaluateResponse, error) {
	if !isChannelLeaderEvaluateResponseBinary(body) {
		return channelLeaderEvaluateResponse{}, fmt.Errorf("access/node: invalid channel leader evaluate response codec")
	}
	offset := len(channelLeaderEvaluateResponseMagic)
	var resp channelLeaderEvaluateResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return channelLeaderEvaluateResponse{}, err
	}
	if resp.Report, offset, err = readChannelLeaderPromotionReportPtr(body, offset); err != nil {
		return channelLeaderEvaluateResponse{}, err
	}
	if offset != len(body) {
		return channelLeaderEvaluateResponse{}, fmt.Errorf("access/node: trailing channel leader evaluate response bytes")
	}
	return resp, nil
}

// encodeChannelLeaderTransferRequestBinary encodes channel leader transfer requests without JSON reflection.
func encodeChannelLeaderTransferRequestBinary(req ChannelLeaderTransferRequest) ([]byte, error) {
	dst := make([]byte, 0, len(channelLeaderTransferRequestMagic)+len(req.ChannelID.ID)+40)
	dst = append(dst, channelLeaderTransferRequestMagic[:]...)
	dst = appendChannelID(dst, req.ChannelID)
	dst = appendUvarint(dst, req.ObservedChannelEpoch)
	dst = appendUvarint(dst, req.ObservedLeaderEpoch)
	dst = appendUvarint(dst, req.TargetNodeID)
	return dst, nil
}

func decodeChannelLeaderTransferRequest(body []byte) (ChannelLeaderTransferRequest, error) {
	if !isChannelLeaderTransferRequestBinary(body) {
		return ChannelLeaderTransferRequest{}, fmt.Errorf("access/node: invalid channel leader transfer request codec")
	}
	offset := len(channelLeaderTransferRequestMagic)
	var req ChannelLeaderTransferRequest
	var err error
	if req.ChannelID, offset, err = readChannelID(body, offset); err != nil {
		return ChannelLeaderTransferRequest{}, err
	}
	if req.ObservedChannelEpoch, offset, err = readUvarint(body, offset); err != nil {
		return ChannelLeaderTransferRequest{}, err
	}
	if req.ObservedLeaderEpoch, offset, err = readUvarint(body, offset); err != nil {
		return ChannelLeaderTransferRequest{}, err
	}
	if req.TargetNodeID, offset, err = readUvarint(body, offset); err != nil {
		return ChannelLeaderTransferRequest{}, err
	}
	if offset != len(body) {
		return ChannelLeaderTransferRequest{}, fmt.Errorf("access/node: trailing channel leader transfer request bytes")
	}
	return req, nil
}

func encodeChannelLeaderTransferResponseBinary(resp channelLeaderTransferResponse) ([]byte, error) {
	dst := make([]byte, 0, len(channelLeaderTransferResponseMagic)+128)
	dst = append(dst, channelLeaderTransferResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendUvarint(dst, resp.LeaderID)
	dst = appendChannelLeaderTransferResultPtr(dst, resp.Result)
	return dst, nil
}

func decodeChannelLeaderTransferResponseBinary(body []byte) (channelLeaderTransferResponse, error) {
	if !isChannelLeaderTransferResponseBinary(body) {
		return channelLeaderTransferResponse{}, fmt.Errorf("access/node: invalid channel leader transfer response codec")
	}
	offset := len(channelLeaderTransferResponseMagic)
	var resp channelLeaderTransferResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return channelLeaderTransferResponse{}, err
	}
	if resp.LeaderID, offset, err = readUvarint(body, offset); err != nil {
		return channelLeaderTransferResponse{}, err
	}
	if resp.Result, offset, err = readChannelLeaderTransferResultPtr(body, offset); err != nil {
		return channelLeaderTransferResponse{}, err
	}
	if offset != len(body) {
		return channelLeaderTransferResponse{}, fmt.Errorf("access/node: trailing channel leader transfer response bytes")
	}
	return resp, nil
}

func isChannelLeaderRepairRequestBinary(body []byte) bool {
	return hasMagic(body, channelLeaderRepairRequestMagic[:])
}

func isChannelLeaderRepairResponseBinary(body []byte) bool {
	return hasMagic(body, channelLeaderRepairResponseMagic[:])
}

func isChannelLeaderEvaluateRequestBinary(body []byte) bool {
	return hasMagic(body, channelLeaderEvaluateRequestMagic[:])
}

func isChannelLeaderEvaluateResponseBinary(body []byte) bool {
	return hasMagic(body, channelLeaderEvaluateResponseMagic[:])
}

func isChannelLeaderTransferRequestBinary(body []byte) bool {
	return hasMagic(body, channelLeaderTransferRequestMagic[:])
}

func isChannelLeaderTransferResponseBinary(body []byte) bool {
	return hasMagic(body, channelLeaderTransferResponseMagic[:])
}

func appendChannelID(dst []byte, id channel.ChannelID) []byte {
	dst = appendString(dst, id.ID)
	return append(dst, id.Type)
}

func readChannelID(body []byte, offset int) (channel.ChannelID, int, error) {
	id, next, err := readString(body, offset)
	if err != nil {
		return channel.ChannelID{}, offset, err
	}
	if next >= len(body) {
		return channel.ChannelID{}, offset, fmt.Errorf("access/node: short channel id type")
	}
	return channel.ChannelID{ID: id, Type: body[next]}, next + 1, nil
}

func appendChannelLeaderRepairResultPtr(dst []byte, result *ChannelLeaderRepairResult) []byte {
	if result == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = appendChannelRuntimeMeta(dst, result.Meta)
	return appendNodeBool(dst, result.Changed)
}

func readChannelLeaderRepairResultPtr(body []byte, offset int) (*ChannelLeaderRepairResult, int, error) {
	marker, next, err := readNodeMarker(body, offset, "channel leader repair result")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	meta, next, err := readChannelRuntimeMeta(body, next)
	if err != nil {
		return nil, offset, err
	}
	changed, next, err := readNodeBool(body, next)
	if err != nil {
		return nil, offset, err
	}
	return &ChannelLeaderRepairResult{Meta: meta, Changed: changed}, next, nil
}

func appendChannelLeaderTransferResultPtr(dst []byte, result *ChannelLeaderTransferResult) []byte {
	if result == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = appendChannelRuntimeMeta(dst, result.Meta)
	return appendNodeBool(dst, result.Changed)
}

func readChannelLeaderTransferResultPtr(body []byte, offset int) (*ChannelLeaderTransferResult, int, error) {
	marker, next, err := readNodeMarker(body, offset, "channel leader transfer result")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	meta, next, err := readChannelRuntimeMeta(body, next)
	if err != nil {
		return nil, offset, err
	}
	changed, next, err := readNodeBool(body, next)
	if err != nil {
		return nil, offset, err
	}
	return &ChannelLeaderTransferResult{Meta: meta, Changed: changed}, next, nil
}

func appendChannelLeaderPromotionReportPtr(dst []byte, report *ChannelLeaderPromotionReport) []byte {
	if report == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = appendUvarint(dst, report.NodeID)
	dst = appendNodeBool(dst, report.Exists)
	dst = appendUvarint(dst, report.ChannelEpoch)
	dst = appendUvarint(dst, report.LocalLEO)
	dst = appendUvarint(dst, report.LocalCheckpointHW)
	dst = appendUvarint(dst, report.LocalOffsetEpoch)
	dst = appendNodeBool(dst, report.CommitReadyNow)
	dst = appendUvarint(dst, report.ProjectedSafeHW)
	dst = appendUvarint(dst, report.ProjectedTruncateTo)
	dst = appendNodeBool(dst, report.CanLead)
	dst = appendString(dst, report.Reason)
	return dst
}

func readChannelLeaderPromotionReportPtr(body []byte, offset int) (*ChannelLeaderPromotionReport, int, error) {
	marker, next, err := readNodeMarker(body, offset, "channel leader promotion report")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	report := &ChannelLeaderPromotionReport{}
	if report.NodeID, next, err = readUvarint(body, next); err != nil {
		return nil, offset, err
	}
	if report.Exists, next, err = readNodeBool(body, next); err != nil {
		return nil, offset, err
	}
	if report.ChannelEpoch, next, err = readUvarint(body, next); err != nil {
		return nil, offset, err
	}
	if report.LocalLEO, next, err = readUvarint(body, next); err != nil {
		return nil, offset, err
	}
	if report.LocalCheckpointHW, next, err = readUvarint(body, next); err != nil {
		return nil, offset, err
	}
	if report.LocalOffsetEpoch, next, err = readUvarint(body, next); err != nil {
		return nil, offset, err
	}
	if report.CommitReadyNow, next, err = readNodeBool(body, next); err != nil {
		return nil, offset, err
	}
	if report.ProjectedSafeHW, next, err = readUvarint(body, next); err != nil {
		return nil, offset, err
	}
	if report.ProjectedTruncateTo, next, err = readUvarint(body, next); err != nil {
		return nil, offset, err
	}
	if report.CanLead, next, err = readNodeBool(body, next); err != nil {
		return nil, offset, err
	}
	if report.Reason, next, err = readString(body, next); err != nil {
		return nil, offset, err
	}
	return report, next, nil
}

func appendChannelRuntimeMeta(dst []byte, meta metadb.ChannelRuntimeMeta) []byte {
	dst = appendString(dst, meta.ChannelID)
	dst = appendNodeVarint(dst, meta.ChannelType)
	dst = appendUvarint(dst, meta.ChannelEpoch)
	dst = appendUvarint(dst, meta.LeaderEpoch)
	dst = appendNodeUint64s(dst, meta.Replicas)
	dst = appendNodeUint64s(dst, meta.ISR)
	dst = appendUvarint(dst, meta.Leader)
	dst = appendNodeVarint(dst, meta.MinISR)
	dst = append(dst, meta.Status)
	dst = appendUvarint(dst, meta.Features)
	dst = appendNodeVarint(dst, meta.LeaseUntilMS)
	dst = appendUvarint(dst, meta.RetentionThroughSeq)
	dst = appendNodeVarint(dst, meta.RetentionUpdatedAtMS)
	return dst
}

func readChannelRuntimeMeta(body []byte, offset int) (metadb.ChannelRuntimeMeta, int, error) {
	var meta metadb.ChannelRuntimeMeta
	var err error
	if meta.ChannelID, offset, err = readString(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.ChannelType, offset, err = readNodeVarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.ChannelEpoch, offset, err = readUvarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.LeaderEpoch, offset, err = readUvarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.Replicas, offset, err = readNodeUint64s(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.ISR, offset, err = readNodeUint64s(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.Leader, offset, err = readUvarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.MinISR, offset, err = readNodeVarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if offset >= len(body) {
		return metadb.ChannelRuntimeMeta{}, offset, fmt.Errorf("access/node: short channel runtime meta status")
	}
	meta.Status = body[offset]
	offset++
	if meta.Features, offset, err = readUvarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.LeaseUntilMS, offset, err = readNodeVarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.RetentionThroughSeq, offset, err = readUvarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.RetentionUpdatedAtMS, offset, err = readNodeVarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	return meta, offset, nil
}

func appendNodeUint64s(dst []byte, values []uint64) []byte {
	dst = appendUvarint(dst, uint64(len(values)))
	for _, value := range values {
		dst = appendUvarint(dst, value)
	}
	return dst
}

func readNodeUint64s(body []byte, offset int) ([]uint64, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	valuesLen, err := readCollectionLen(count, len(body)-offset, "channel leader uint64 list")
	if err != nil {
		return nil, offset, err
	}
	values := make([]uint64, valuesLen)
	for i := range values {
		if values[i], offset, err = readUvarint(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return values, offset, nil
}

func appendNodeBool(dst []byte, v bool) []byte {
	if v {
		return append(dst, 1)
	}
	return append(dst, 0)
}

func readNodeBool(body []byte, offset int) (bool, int, error) {
	if offset >= len(body) {
		return false, offset, fmt.Errorf("access/node: short bool")
	}
	return body[offset] != 0, offset + 1, nil
}

func readNodeMarker(body []byte, offset int, label string) (byte, int, error) {
	if offset >= len(body) {
		return 0, offset, fmt.Errorf("access/node: short %s marker", label)
	}
	marker := body[offset]
	if marker > 1 {
		return 0, offset, fmt.Errorf("access/node: invalid %s marker", label)
	}
	return marker, offset + 1, nil
}

func appendNodeVarint(dst []byte, v int64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutVarint(buf[:], v)
	return append(dst, buf[:n]...)
}

func readNodeVarint(body []byte, offset int) (int64, int, error) {
	if offset >= len(body) {
		return 0, offset, fmt.Errorf("access/node: short varint")
	}
	v, n := binary.Varint(body[offset:])
	if n <= 0 {
		return 0, offset, fmt.Errorf("access/node: invalid varint")
	}
	return v, offset + n, nil
}
