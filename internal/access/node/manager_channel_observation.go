package node

import (
	"context"
	"fmt"
	"time"

	management "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

var (
	managerObservationRequestMagic  = [...]byte{'W', 'K', 'V', 'A', 1}
	managerObservationResponseMagic = [...]byte{'W', 'K', 'V', 'a', 1}
)

const maxManagerObservationBytes = 512 * 1024
const maxManagerObservations = 200

type managerObservationRequest struct {
	Targets              []management.ChannelObservationTarget
	IncludeMaxMessageSeq bool
}
type managerObservationResponse struct {
	Status string
	Items  []management.ChannelObservation
}

// SetManagerChannelObservations wires the observation port before RPC admission starts.
func (a *Adapter) SetManagerChannelObservations(observer management.LocalChannelObserver) {
	a.managerChannelObservations = observer
}

func (a *Adapter) handleManagerObservationRPC(parent context.Context, payload []byte) ([]byte, error) {
	req, err := decodeManagerObservationRequest(payload)
	if err != nil {
		return nil, err
	}
	if a == nil || a.managerChannelObservations == nil {
		return encodeManagerObservationResponse(managerObservationResponse{Status: rpcStatusRejected})
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	rows, err := a.managerChannelObservations.ObserveLocalChannels(ctx, req.Targets, req.IncludeMaxMessageSeq)
	if err != nil {
		return encodeManagerObservationResponse(managerObservationResponse{Status: managerChannelRPCStatusForError(err)})
	}
	if err := validateManagerObservationAlignment(req.Targets, rows, req.IncludeMaxMessageSeq); err != nil {
		return nil, err
	}
	return encodeManagerObservationResponse(managerObservationResponse{Status: rpcStatusOK, Items: rows})
}

// ObserveManagerChannels invokes the separate read-only observation protocol.
// Unsupported older peers fail explicitly; no activating fallback is permitted.
func (c *Client) ObserveManagerChannels(ctx context.Context, nodeID uint64, targets []management.ChannelObservationTarget, include bool) ([]management.ChannelObservation, error) {
	if c == nil || c.node == nil || nodeID == 0 {
		return nil, management.ErrBusinessChannelReaderUnavailable
	}
	for _, target := range targets {
		if target.LeaderID != nodeID {
			return nil, management.ErrBusinessChannelReaderUnavailable
		}
	}
	body, err := encodeManagerObservationRequest(managerObservationRequest{Targets: targets, IncludeMaxMessageSeq: include})
	if err != nil {
		return nil, err
	}
	reply, err := c.node.CallRPC(ctx, nodeID, ManagerChannelRPCServiceID, body)
	if err != nil {
		return nil, err
	}
	resp, err := decodeManagerObservationResponse(reply)
	if err != nil {
		return nil, err
	}
	if err := managerChannelRPCErrorForStatus(resp.Status); err != nil {
		return nil, err
	}
	if err := validateManagerObservationAlignment(targets, resp.Items, include); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func validateManagerObservationAlignment(targets []management.ChannelObservationTarget, rows []management.ChannelObservation, include bool) error {
	if len(rows) != len(targets) {
		return fmt.Errorf("manager channel observation: misaligned response")
	}
	for i, row := range rows {
		if row.Target != targets[i] || (!row.Known && (row.Active || row.MaxMessageSeq != nil)) || (!include && row.MaxMessageSeq != nil) {
			return fmt.Errorf("manager channel observation: invalid response evidence")
		}
	}
	return nil
}

func validManagerObservationTarget(target management.ChannelObservationTarget) bool {
	return target.ChannelID != "" && len(target.ChannelID) <= 4096 && target.ChannelType > 0 && target.ChannelType <= 255 && target.LeaderID != 0
}

func appendManagerObservationTarget(body []byte, target management.ChannelObservationTarget) []byte {
	body = appendString(body, target.ChannelID)
	for _, value := range []uint64{uint64(target.ChannelType), target.LeaderID, target.Epoch, target.LeaderEpoch, target.RouteGeneration} {
		body = appendUvarint(body, value)
	}
	return body
}

func readManagerObservationTarget(body []byte, offset int) (management.ChannelObservationTarget, int, error) {
	var target management.ChannelObservationTarget
	var err error
	target.ChannelID, offset, err = readString(body, offset)
	if err != nil {
		return target, offset, err
	}
	values := [5]uint64{}
	for i := range values {
		values[i], offset, err = readUvarint(body, offset)
		if err != nil {
			return target, offset, err
		}
	}
	if values[0] > 255 {
		return target, offset, fmt.Errorf("manager channel observation: invalid channel type")
	}
	target.ChannelType = int64(values[0])
	target.LeaderID = values[1]
	target.Epoch = values[2]
	target.LeaderEpoch = values[3]
	target.RouteGeneration = values[4]
	if !validManagerObservationTarget(target) {
		return target, offset, fmt.Errorf("manager channel observation: invalid target")
	}
	return target, offset, nil
}

func encodeManagerObservationRequest(req managerObservationRequest) ([]byte, error) {
	if len(req.Targets) > maxManagerObservations {
		return nil, fmt.Errorf("manager channel observation: oversized batch")
	}
	body := append([]byte(nil), managerObservationRequestMagic[:]...)
	body = appendBoolByte(body, req.IncludeMaxMessageSeq)
	body = appendUvarint(body, uint64(len(req.Targets)))
	for _, target := range req.Targets {
		if !validManagerObservationTarget(target) {
			return nil, fmt.Errorf("manager channel observation: invalid target")
		}
		body = appendManagerObservationTarget(body, target)
	}
	if len(body) > maxManagerObservationBytes {
		return nil, fmt.Errorf("manager channel observation: oversized frame")
	}
	return body, nil
}

func decodeManagerObservationRequest(body []byte) (managerObservationRequest, error) {
	var req managerObservationRequest
	if len(body) > maxManagerObservationBytes || !hasMagic(body, managerObservationRequestMagic[:]) {
		return req, fmt.Errorf("manager channel observation: invalid request frame")
	}
	offset := len(managerObservationRequestMagic)
	var err error
	req.IncludeMaxMessageSeq, offset, err = readBoolByte(body, offset, "include sequence")
	if err != nil {
		return req, err
	}
	n, offset, err := readUvarint(body, offset)
	if err != nil || n > maxManagerObservations {
		return req, fmt.Errorf("manager channel observation: invalid request count")
	}
	req.Targets = make([]management.ChannelObservationTarget, n)
	for i := range req.Targets {
		req.Targets[i], offset, err = readManagerObservationTarget(body, offset)
		if err != nil {
			return req, err
		}
	}
	if offset != len(body) {
		return req, fmt.Errorf("manager channel observation: trailing request bytes")
	}
	return req, nil
}

func encodeManagerObservationResponse(resp managerObservationResponse) ([]byte, error) {
	if len(resp.Items) > maxManagerObservations || (resp.Status != rpcStatusOK && len(resp.Items) != 0) {
		return nil, fmt.Errorf("manager channel observation: invalid response count")
	}
	body := append([]byte(nil), managerObservationResponseMagic[:]...)
	body = appendString(body, resp.Status)
	body = appendUvarint(body, uint64(len(resp.Items)))
	for _, row := range resp.Items {
		if !validManagerObservationTarget(row.Target) || (!row.Known && (row.Active || row.MaxMessageSeq != nil)) {
			return nil, fmt.Errorf("manager channel observation: invalid evidence")
		}
		body = appendManagerObservationTarget(body, row.Target)
		body = appendBoolByte(body, row.Known)
		body = appendBoolByte(body, row.Active)
		body = appendBoolByte(body, row.MaxMessageSeq != nil)
		if row.MaxMessageSeq != nil {
			body = appendUvarint(body, *row.MaxMessageSeq)
		}
	}
	if len(body) > maxManagerObservationBytes {
		return nil, fmt.Errorf("manager channel observation: oversized response")
	}
	return body, nil
}

func decodeManagerObservationResponse(body []byte) (managerObservationResponse, error) {
	var resp managerObservationResponse
	if len(body) > maxManagerObservationBytes || !hasMagic(body, managerObservationResponseMagic[:]) {
		return resp, fmt.Errorf("manager channel observation: invalid response frame")
	}
	offset := len(managerObservationResponseMagic)
	var err error
	resp.Status, offset, err = readString(body, offset)
	if err != nil {
		return resp, err
	}
	n, offset, err := readUvarint(body, offset)
	if err != nil || n > maxManagerObservations || (resp.Status != rpcStatusOK && n != 0) {
		return resp, fmt.Errorf("manager channel observation: invalid response count")
	}
	resp.Items = make([]management.ChannelObservation, n)
	for i := range resp.Items {
		row := &resp.Items[i]
		row.Target, offset, err = readManagerObservationTarget(body, offset)
		if err != nil {
			return resp, err
		}
		row.Known, offset, err = readBoolByte(body, offset, "known")
		if err != nil {
			return resp, err
		}
		row.Active, offset, err = readBoolByte(body, offset, "active")
		if err != nil {
			return resp, err
		}
		var hasSeq bool
		hasSeq, offset, err = readBoolByte(body, offset, "sequence present")
		if err != nil {
			return resp, err
		}
		if hasSeq {
			var seq uint64
			seq, offset, err = readUvarint(body, offset)
			if err != nil {
				return resp, err
			}
			row.MaxMessageSeq = &seq
		}
		if !row.Known && (row.Active || hasSeq) {
			return resp, fmt.Errorf("manager channel observation: contradictory evidence")
		}
	}
	if offset != len(body) {
		return resp, fmt.Errorf("manager channel observation: trailing response bytes")
	}
	return resp, nil
}
