package node

import "fmt"

var (
	channelRetentionRequestMagic  = [...]byte{'W', 'K', 'C', 'R', 1}
	channelRetentionResponseMagic = [...]byte{'W', 'K', 'C', 'T', 1}
)

// encodeChannelRetentionRequestBinary encodes channel retention commands without JSON reflection.
func encodeChannelRetentionRequestBinary(req channelRetentionRequest) ([]byte, error) {
	dst := make([]byte, 0, len(channelRetentionRequestMagic)+len(req.Request.ChannelID.ID)+32)
	dst = append(dst, channelRetentionRequestMagic[:]...)
	dst = appendChannelRetentionAdvanceRequest(dst, req.Request)
	return dst, nil
}

func decodeChannelRetentionRequest(body []byte) (channelRetentionRequest, error) {
	if !isChannelRetentionRequestBinary(body) {
		return channelRetentionRequest{}, fmt.Errorf("access/node: invalid channel retention request codec")
	}
	offset := len(channelRetentionRequestMagic)
	req, next, err := readChannelRetentionAdvanceRequest(body, offset)
	if err != nil {
		return channelRetentionRequest{}, err
	}
	if next != len(body) {
		return channelRetentionRequest{}, fmt.Errorf("access/node: trailing channel retention request bytes")
	}
	return channelRetentionRequest{Request: req}, nil
}

func encodeChannelRetentionResponseBinary(resp channelRetentionResponse) ([]byte, error) {
	dst := make([]byte, 0, len(channelRetentionResponseMagic)+len(resp.Result.ChannelID.ID)+128)
	dst = append(dst, channelRetentionResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendUvarint(dst, resp.LeaderID)
	dst = appendChannelRetentionAdvanceResult(dst, resp.Result)
	return dst, nil
}

func decodeChannelRetentionResponseBinary(body []byte) (channelRetentionResponse, error) {
	if !isChannelRetentionResponseBinary(body) {
		return channelRetentionResponse{}, fmt.Errorf("access/node: invalid channel retention response codec")
	}
	offset := len(channelRetentionResponseMagic)
	var resp channelRetentionResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return channelRetentionResponse{}, err
	}
	if resp.LeaderID, offset, err = readUvarint(body, offset); err != nil {
		return channelRetentionResponse{}, err
	}
	if resp.Result, offset, err = readChannelRetentionAdvanceResult(body, offset); err != nil {
		return channelRetentionResponse{}, err
	}
	if offset != len(body) {
		return channelRetentionResponse{}, fmt.Errorf("access/node: trailing channel retention response bytes")
	}
	return resp, nil
}

func isChannelRetentionRequestBinary(body []byte) bool {
	return hasMagic(body, channelRetentionRequestMagic[:])
}

func isChannelRetentionResponseBinary(body []byte) bool {
	return hasMagic(body, channelRetentionResponseMagic[:])
}

func appendChannelRetentionAdvanceRequest(dst []byte, req ChannelRetentionAdvanceRequest) []byte {
	dst = appendChannelID(dst, req.ChannelID)
	dst = appendUvarint(dst, req.ThroughSeq)
	return appendNodeBool(dst, req.DryRun)
}

func readChannelRetentionAdvanceRequest(body []byte, offset int) (ChannelRetentionAdvanceRequest, int, error) {
	var req ChannelRetentionAdvanceRequest
	var err error
	if req.ChannelID, offset, err = readChannelID(body, offset); err != nil {
		return ChannelRetentionAdvanceRequest{}, offset, err
	}
	if req.ThroughSeq, offset, err = readUvarint(body, offset); err != nil {
		return ChannelRetentionAdvanceRequest{}, offset, err
	}
	if req.DryRun, offset, err = readNodeBool(body, offset); err != nil {
		return ChannelRetentionAdvanceRequest{}, offset, err
	}
	return req, offset, nil
}

func appendChannelRetentionAdvanceResult(dst []byte, result ChannelRetentionAdvanceResult) []byte {
	dst = appendChannelID(dst, result.ChannelID)
	dst = appendUvarint(dst, result.RequestedThroughSeq)
	dst = appendUvarint(dst, result.AdvancedThroughSeq)
	dst = appendUvarint(dst, result.MinAvailableSeq)
	dst = appendString(dst, result.Status)
	return appendString(dst, result.BlockedReason)
}

func readChannelRetentionAdvanceResult(body []byte, offset int) (ChannelRetentionAdvanceResult, int, error) {
	var result ChannelRetentionAdvanceResult
	var err error
	if result.ChannelID, offset, err = readChannelID(body, offset); err != nil {
		return ChannelRetentionAdvanceResult{}, offset, err
	}
	if result.RequestedThroughSeq, offset, err = readUvarint(body, offset); err != nil {
		return ChannelRetentionAdvanceResult{}, offset, err
	}
	if result.AdvancedThroughSeq, offset, err = readUvarint(body, offset); err != nil {
		return ChannelRetentionAdvanceResult{}, offset, err
	}
	if result.MinAvailableSeq, offset, err = readUvarint(body, offset); err != nil {
		return ChannelRetentionAdvanceResult{}, offset, err
	}
	if result.Status, offset, err = readString(body, offset); err != nil {
		return ChannelRetentionAdvanceResult{}, offset, err
	}
	if result.BlockedReason, offset, err = readString(body, offset); err != nil {
		return ChannelRetentionAdvanceResult{}, offset, err
	}
	return result, offset, nil
}
