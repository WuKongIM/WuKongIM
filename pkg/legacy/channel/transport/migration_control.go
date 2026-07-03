package transport

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel/runtime"
)

// FenceAndDrain sends a migration drain request to the peer that owns the channel leader.
func (t *Transport) FenceAndDrain(ctx context.Context, peer channel.NodeID, req channel.FenceAndDrainRequest) (channel.DrainResult, error) {
	body, err := encodeFenceAndDrainRequest(req)
	if err != nil {
		return channel.DrainResult{}, err
	}
	respBody, err := t.client.RPCService(ctx, uint64(peer), fetchRPCShardKey(req.ChannelKey), RPCServiceFenceAndDrain, body)
	if err != nil {
		return channel.DrainResult{}, normalizeMigrationControlError(err)
	}
	return decodeFenceAndDrainResponse(respBody)
}

func (t *Transport) handleMigrationControlDrainRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeFenceAndDrainRequest(body)
	if err != nil {
		return nil, err
	}
	service, err := t.boundMigrationRuntime()
	if err != nil {
		return nil, err
	}
	result, err := service.FenceAndDrain(ctx, req)
	if err != nil {
		return nil, err
	}
	return encodeFenceAndDrainResponse(result)
}

func (t *Transport) boundMigrationRuntime() (runtime.MigrationRuntime, error) {
	t.mu.RLock()
	service := t.migrationService
	t.mu.RUnlock()
	if service == nil {
		return nil, fmt.Errorf("channeltransport: migration runtime must be bound")
	}
	return service, nil
}

func encodeFenceAndDrainRequest(req channel.FenceAndDrainRequest) ([]byte, error) {
	buf := bytes.NewBuffer(make([]byte, 0, 96))
	buf.WriteByte(fenceAndDrainReqVer)
	if err := writeChannelKey(buf, req.ChannelKey); err != nil {
		return nil, err
	}
	if err := writeString(buf, req.TaskID); err != nil {
		return nil, err
	}
	if err := writeString(buf, req.WriteFenceToken); err != nil {
		return nil, err
	}
	for _, value := range []uint64{
		req.WriteFenceVersion,
		req.ExpectedChannelEpoch,
		req.ExpectedLeaderEpoch,
		uint64(req.ExpectedLeader),
	} {
		if err := binary.Write(buf, binary.BigEndian, value); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func decodeFenceAndDrainRequest(data []byte) (channel.FenceAndDrainRequest, error) {
	rd := bytes.NewReader(data)
	version, err := rd.ReadByte()
	if err != nil {
		return channel.FenceAndDrainRequest{}, err
	}
	if version != fenceAndDrainReqVer {
		return channel.FenceAndDrainRequest{}, fmt.Errorf("channeltransport: unknown fence drain request codec version %d", version)
	}
	var req channel.FenceAndDrainRequest
	if req.ChannelKey, err = readChannelKey(rd); err != nil {
		return channel.FenceAndDrainRequest{}, err
	}
	if req.TaskID, err = readString(rd); err != nil {
		return channel.FenceAndDrainRequest{}, err
	}
	if req.WriteFenceToken, err = readString(rd); err != nil {
		return channel.FenceAndDrainRequest{}, err
	}
	values := []*uint64{&req.WriteFenceVersion, &req.ExpectedChannelEpoch, &req.ExpectedLeaderEpoch}
	for _, target := range values {
		if err := binary.Read(rd, binary.BigEndian, target); err != nil {
			return channel.FenceAndDrainRequest{}, err
		}
	}
	var leader uint64
	if err := binary.Read(rd, binary.BigEndian, &leader); err != nil {
		return channel.FenceAndDrainRequest{}, err
	}
	req.ExpectedLeader = channel.NodeID(leader)
	if rd.Len() != 0 {
		return channel.FenceAndDrainRequest{}, fmt.Errorf("channeltransport: trailing fence drain request bytes")
	}
	return req, nil
}

func encodeFenceAndDrainResponse(result channel.DrainResult) ([]byte, error) {
	buf := bytes.NewBuffer(make([]byte, 0, 80))
	buf.WriteByte(fenceAndDrainRespVer)
	if err := writeChannelKey(buf, result.ChannelKey); err != nil {
		return nil, err
	}
	for _, value := range []uint64{
		result.LEO,
		result.HW,
		result.CheckpointHW,
		result.ChannelEpoch,
		result.LeaderEpoch,
		result.WriteFenceVersion,
		result.RuntimeGeneration,
	} {
		if err := binary.Write(buf, binary.BigEndian, value); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func decodeFenceAndDrainResponse(data []byte) (channel.DrainResult, error) {
	rd := bytes.NewReader(data)
	version, err := rd.ReadByte()
	if err != nil {
		return channel.DrainResult{}, err
	}
	if version != fenceAndDrainRespVer {
		return channel.DrainResult{}, fmt.Errorf("channeltransport: unknown fence drain response codec version %d", version)
	}
	var result channel.DrainResult
	if result.ChannelKey, err = readChannelKey(rd); err != nil {
		return channel.DrainResult{}, err
	}
	values := []*uint64{
		&result.LEO,
		&result.HW,
		&result.CheckpointHW,
		&result.ChannelEpoch,
		&result.LeaderEpoch,
		&result.WriteFenceVersion,
		&result.RuntimeGeneration,
	}
	for _, target := range values {
		if err := binary.Read(rd, binary.BigEndian, target); err != nil {
			return channel.DrainResult{}, err
		}
	}
	if rd.Len() != 0 {
		return channel.DrainResult{}, fmt.Errorf("channeltransport: trailing fence drain response bytes")
	}
	return result, nil
}

func writeString(buf *bytes.Buffer, value string) error {
	if err := binary.Write(buf, binary.BigEndian, uint32(len(value))); err != nil {
		return err
	}
	_, err := io.WriteString(buf, value)
	return err
}

func readString(rd *bytes.Reader) (string, error) {
	var length uint32
	if err := binary.Read(rd, binary.BigEndian, &length); err != nil {
		return "", err
	}
	value := make([]byte, length)
	if _, err := io.ReadFull(rd, value); err != nil {
		return "", err
	}
	return string(value), nil
}

func normalizeMigrationControlError(err error) error {
	if err == nil {
		return nil
	}
	for _, target := range []error{
		channel.ErrNotLeader,
		channel.ErrStaleMeta,
		channel.ErrWriteFenced,
		channel.ErrNotReady,
		channel.ErrLeaseExpired,
		channel.ErrChannelNotFound,
	} {
		if errors.Is(err, target) || strings.Contains(err.Error(), target.Error()) {
			return target
		}
	}
	return err
}
