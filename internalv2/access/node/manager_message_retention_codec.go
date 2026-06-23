package node

import (
	"fmt"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

var (
	managerMessageRetentionRequestMagic  = [...]byte{'W', 'K', 'V', 'T', 1}
	managerMessageRetentionResponseMagic = [...]byte{'W', 'K', 'V', 't', 1}
)

type managerMessageRetentionRPCRequest struct {
	Request managementusecase.AdvanceMessageRetentionRequest
}

type managerMessageRetentionRPCResponse struct {
	Status string
	Result managementusecase.AdvanceMessageRetentionResponse
}

func encodeManagerMessageRetentionRequest(req managerMessageRetentionRPCRequest) ([]byte, error) {
	dst := make([]byte, 0, 96)
	dst = append(dst, managerMessageRetentionRequestMagic[:]...)
	dst = appendString(dst, req.Request.ChannelID)
	dst = appendVarint(dst, req.Request.ChannelType)
	dst = appendUvarint(dst, req.Request.ThroughSeq)
	return appendBoolByte(dst, req.Request.DryRun), nil
}

func decodeManagerMessageRetentionRequest(body []byte) (managerMessageRetentionRPCRequest, error) {
	if !hasMagic(body, managerMessageRetentionRequestMagic[:]) {
		return managerMessageRetentionRPCRequest{}, fmt.Errorf("internalv2/access/node: invalid manager message retention request codec")
	}
	offset := len(managerMessageRetentionRequestMagic)
	channelID, offset, err := readString(body, offset)
	if err != nil {
		return managerMessageRetentionRPCRequest{}, err
	}
	channelType, offset, err := readVarint(body, offset)
	if err != nil {
		return managerMessageRetentionRPCRequest{}, err
	}
	throughSeq, offset, err := readUvarint(body, offset)
	if err != nil {
		return managerMessageRetentionRPCRequest{}, err
	}
	dryRun, offset, err := readBoolByte(body, offset, "manager message retention dry_run")
	if err != nil {
		return managerMessageRetentionRPCRequest{}, err
	}
	if offset != len(body) {
		return managerMessageRetentionRPCRequest{}, fmt.Errorf("internalv2/access/node: trailing manager message retention request bytes")
	}
	return managerMessageRetentionRPCRequest{Request: managementusecase.AdvanceMessageRetentionRequest{
		ChannelID: channelID, ChannelType: channelType, ThroughSeq: throughSeq, DryRun: dryRun,
	}}, nil
}

func encodeManagerMessageRetentionResponse(resp managerMessageRetentionRPCResponse) ([]byte, error) {
	dst := make([]byte, 0, 160)
	dst = append(dst, managerMessageRetentionResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendString(dst, resp.Result.ChannelID)
	dst = appendVarint(dst, resp.Result.ChannelType)
	dst = appendUvarint(dst, resp.Result.RequestedThroughSeq)
	dst = appendUvarint(dst, resp.Result.AdvancedThroughSeq)
	dst = appendUvarint(dst, resp.Result.MinAvailableSeq)
	dst = appendString(dst, string(resp.Result.Status))
	return appendString(dst, string(resp.Result.BlockedReason)), nil
}

func decodeManagerMessageRetentionResponse(body []byte) (managerMessageRetentionRPCResponse, error) {
	if !hasMagic(body, managerMessageRetentionResponseMagic[:]) {
		return managerMessageRetentionRPCResponse{}, fmt.Errorf("internalv2/access/node: invalid manager message retention response codec")
	}
	offset := len(managerMessageRetentionResponseMagic)
	status, offset, err := readString(body, offset)
	if err != nil {
		return managerMessageRetentionRPCResponse{}, err
	}
	var result managementusecase.AdvanceMessageRetentionResponse
	if result.ChannelID, offset, err = readString(body, offset); err != nil {
		return managerMessageRetentionRPCResponse{}, err
	}
	if result.ChannelType, offset, err = readVarint(body, offset); err != nil {
		return managerMessageRetentionRPCResponse{}, err
	}
	if result.RequestedThroughSeq, offset, err = readUvarint(body, offset); err != nil {
		return managerMessageRetentionRPCResponse{}, err
	}
	if result.AdvancedThroughSeq, offset, err = readUvarint(body, offset); err != nil {
		return managerMessageRetentionRPCResponse{}, err
	}
	if result.MinAvailableSeq, offset, err = readUvarint(body, offset); err != nil {
		return managerMessageRetentionRPCResponse{}, err
	}
	retentionStatus, offset, err := readString(body, offset)
	if err != nil {
		return managerMessageRetentionRPCResponse{}, err
	}
	blockedReason, offset, err := readString(body, offset)
	if err != nil {
		return managerMessageRetentionRPCResponse{}, err
	}
	if offset != len(body) {
		return managerMessageRetentionRPCResponse{}, fmt.Errorf("internalv2/access/node: trailing manager message retention response bytes")
	}
	result.Status = managementusecase.MessageRetentionStatus(retentionStatus)
	result.BlockedReason = managementusecase.MessageRetentionBlockedReason(blockedReason)
	return managerMessageRetentionRPCResponse{Status: status, Result: result}, nil
}
