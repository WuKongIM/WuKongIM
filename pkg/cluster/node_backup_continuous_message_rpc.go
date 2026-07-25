package cluster

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

type backupContinuousMessageRPCRequest struct {
	Action      string                        `json:"action"`
	Channel     BackupMessageChannelRequest   `json:"channel,omitempty"`
	Channels    []BackupMessageChannelRequest `json:"channels,omitempty"`
	Page        BackupMessageLogPageRequest   `json:"page,omitempty"`
	ChunkOffset int                           `json:"chunk_offset,omitempty"`
}

type backupContinuousMessageRPCResponse struct {
	Boundary   BackupMessageChannelBoundary   `json:"boundary,omitempty"`
	Boundaries []BackupMessageChannelBoundary `json:"boundaries,omitempty"`
}

type backupContinuousMessageRPCChunk struct {
	TotalBytes int    `json:"total_bytes"`
	Offset     int    `json:"offset"`
	Data       []byte `json:"data"`
}

type backupContinuousMessageRPCHandler struct {
	node *Node
}

func (h backupContinuousMessageRPCHandler) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	var request backupContinuousMessageRPCRequest
	if err := decodeBackupContinuousMessageJSON(payload, &request); err != nil {
		return nil, err
	}
	response := backupContinuousMessageRPCResponse{}
	var err error
	switch request.Action {
	case backupContinuousMessageActionObserve:
		if request.Channel.LeaderNodeID != h.node.NodeID() {
			return nil, channelruntime.ErrStaleMeta
		}
		if err := validateBackupMessageChannelRequest(h.node, request.Channel); err != nil {
			return nil, err
		}
		response.Boundary, err = h.node.observeBackupMessageChannelLocal(ctx, request.Channel)
	case backupContinuousMessageActionObserveBatch:
		if len(request.Channels) == 0 || len(request.Channels) > backupContinuousMessageBatchChannels {
			return nil, channelruntime.ErrInvalidConfig
		}
		for _, channel := range request.Channels {
			if channel.LeaderNodeID != h.node.NodeID() {
				return nil, channelruntime.ErrStaleMeta
			}
			if err := validateBackupMessageChannelRequest(h.node, channel); err != nil {
				return nil, err
			}
		}
		response.Boundaries, err = h.node.observeBackupMessageChannelsLocal(ctx, request.Channels)
	case backupContinuousMessageActionRead:
		if request.Page.Channel.LeaderNodeID != h.node.NodeID() {
			return nil, channelruntime.ErrStaleMeta
		}
		if err := validateBackupMessageLogPageRequest(h.node, request.Page); err != nil {
			return nil, err
		}
		if request.ChunkOffset < 0 {
			return nil, channelruntime.ErrInvalidConfig
		}
		key, keyErr := backupContinuousChunkKey(backupContinuousChunkKindMessage, request.Page)
		if keyErr != nil {
			return nil, keyErr
		}
		totalBytes, data, _, validChunk, loadErr := h.node.backupContinuousChunks.chunk(
			ctx, key, request.ChunkOffset, backupContinuousMessageChunkBytes,
			func(ctx context.Context) ([]byte, error) {
				page, err := h.node.readBackupMessageLogPageLocal(ctx, request.Page)
				if err != nil {
					return nil, err
				}
				page.Boundary.ObservedAtUnixMillis = 0
				return marshalBackupMessageLogPage(page)
			},
		)
		if loadErr != nil {
			return nil, loadErr
		}
		if !validChunk {
			return nil, channelruntime.ErrInvalidConfig
		}
		responseBody, marshalErr := json.Marshal(backupContinuousMessageRPCChunk{
			TotalBytes: totalBytes, Offset: request.ChunkOffset, Data: data,
		})
		return responseBody, marshalErr
	default:
		err = channelruntime.ErrInvalidConfig
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(response)
}

func (n *Node) callBackupContinuousMessage(ctx context.Context, nodeID uint64, request backupContinuousMessageRPCRequest) (backupContinuousMessageRPCResponse, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return backupContinuousMessageRPCResponse{}, err
	}
	responseBody, err := n.CallRPC(ctx, nodeID, clusternet.RPCBackupContinuousMessage, body)
	if err != nil {
		return backupContinuousMessageRPCResponse{}, err
	}
	var response backupContinuousMessageRPCResponse
	if err := decodeBackupContinuousMessageJSON(responseBody, &response); err != nil {
		return backupContinuousMessageRPCResponse{}, err
	}
	return response, nil
}

func (n *Node) callBackupContinuousMessagePage(ctx context.Context, request BackupMessageLogPageRequest) (BackupMessageLogPage, error) {
	var assembled []byte
	total := 0
	for offset := 0; ; {
		body, err := json.Marshal(backupContinuousMessageRPCRequest{
			Action: backupContinuousMessageActionRead, Page: request, ChunkOffset: offset,
		})
		if err != nil {
			return BackupMessageLogPage{}, err
		}
		responseBody, err := n.CallRPC(ctx, request.Channel.LeaderNodeID, clusternet.RPCBackupContinuousMessage, body)
		if err != nil {
			return BackupMessageLogPage{}, err
		}
		var chunk backupContinuousMessageRPCChunk
		if err := decodeBackupContinuousMessageJSON(responseBody, &chunk); err != nil {
			return BackupMessageLogPage{}, err
		}
		if chunk.TotalBytes <= 0 || chunk.TotalBytes > int(MaxCaptureBackupRecordBytes)+(1<<20) ||
			chunk.Offset != offset || len(chunk.Data) == 0 ||
			offset > chunk.TotalBytes-len(chunk.Data) {
			return BackupMessageLogPage{}, channelruntime.ErrInvalidConfig
		}
		if total == 0 {
			total = chunk.TotalBytes
			assembled = make([]byte, 0, total)
		} else if chunk.TotalBytes != total {
			return BackupMessageLogPage{}, channelruntime.ErrStaleMeta
		}
		assembled = append(assembled, chunk.Data...)
		offset += len(chunk.Data)
		if offset == total {
			page, err := loadBackupMessageLogPage(assembled)
			if err != nil {
				return BackupMessageLogPage{}, err
			}
			if err := validateBackupMessageChannelBoundary(request.Channel, page.Boundary, false); err != nil ||
				len(page.Records) > request.MaxRecords ||
				page.NextSeq <= request.FromSeq ||
				page.NextSeq > request.ThroughSeq+1 ||
				page.Done != (page.NextSeq > request.ThroughSeq) ||
				page.Boundary.HW != page.NextSeq-1 {
				return BackupMessageLogPage{}, channelruntime.ErrStaleMeta
			}
			return page, nil
		}
	}
}

var backupMessageLogPageMagic = [4]byte{'W', 'K', 'B', 'P'}

func marshalBackupMessageLogPage(page BackupMessageLogPage) ([]byte, error) {
	if len(page.Boundary.ChannelID) == 0 || len(page.Boundary.ChannelID) > 4<<10 ||
		len(page.Records) == 0 || page.NextSeq == 0 {
		return nil, channelruntime.ErrInvalidConfig
	}
	total := 4 + 2 + 2 + 1 + 2 + len(page.Boundary.ChannelID) + 8*5 + 1 + 4
	for _, record := range page.Records {
		if len(record) == 0 || total > int(MaxCaptureBackupRecordBytes)+(1<<20)-4-len(record) {
			return nil, channelruntime.ErrInvalidConfig
		}
		total += 4 + len(record)
	}
	body := make([]byte, 0, total)
	body = append(body, backupMessageLogPageMagic[:]...)
	body = binary.BigEndian.AppendUint16(body, 1)
	body = binary.BigEndian.AppendUint16(body, page.Boundary.HashSlot)
	body = append(body, page.Boundary.ChannelType)
	body = binary.BigEndian.AppendUint16(body, uint16(len(page.Boundary.ChannelID)))
	body = append(body, page.Boundary.ChannelID...)
	body = binary.BigEndian.AppendUint64(body, page.Boundary.Epoch)
	body = binary.BigEndian.AppendUint64(body, page.Boundary.LogStartOffset)
	body = binary.BigEndian.AppendUint64(body, page.Boundary.HW)
	body = binary.BigEndian.AppendUint64(body, uint64(page.Boundary.ObservedAtUnixMillis))
	body = binary.BigEndian.AppendUint64(body, page.NextSeq)
	if page.Done {
		body = append(body, 1)
	} else {
		body = append(body, 0)
	}
	body = binary.BigEndian.AppendUint32(body, uint32(len(page.Records)))
	for _, record := range page.Records {
		body = binary.BigEndian.AppendUint32(body, uint32(len(record)))
		body = append(body, record...)
	}
	return body, nil
}

func loadBackupMessageLogPage(body []byte) (BackupMessageLogPage, error) {
	if len(body) < 4+2+2+1+2+8*5+1+4 || len(body) > int(MaxCaptureBackupRecordBytes)+(1<<20) ||
		!bytes.Equal(body[:4], backupMessageLogPageMagic[:]) ||
		binary.BigEndian.Uint16(body[4:6]) != 1 {
		return BackupMessageLogPage{}, channelruntime.ErrInvalidConfig
	}
	reader := bytes.NewReader(body[6:])
	var page BackupMessageLogPage
	var idBytes uint16
	var observedAt uint64
	var done uint8
	var count uint32
	if binary.Read(reader, binary.BigEndian, &page.Boundary.HashSlot) != nil ||
		binary.Read(reader, binary.BigEndian, &page.Boundary.ChannelType) != nil ||
		binary.Read(reader, binary.BigEndian, &idBytes) != nil ||
		idBytes == 0 || idBytes > 4<<10 || int(idBytes) > reader.Len() {
		return BackupMessageLogPage{}, channelruntime.ErrInvalidConfig
	}
	id := make([]byte, idBytes)
	if _, err := io.ReadFull(reader, id); err != nil {
		return BackupMessageLogPage{}, channelruntime.ErrInvalidConfig
	}
	page.Boundary.ChannelID = string(id)
	if binary.Read(reader, binary.BigEndian, &page.Boundary.Epoch) != nil ||
		binary.Read(reader, binary.BigEndian, &page.Boundary.LogStartOffset) != nil ||
		binary.Read(reader, binary.BigEndian, &page.Boundary.HW) != nil ||
		binary.Read(reader, binary.BigEndian, &observedAt) != nil ||
		observedAt > math.MaxInt64 ||
		binary.Read(reader, binary.BigEndian, &page.NextSeq) != nil ||
		binary.Read(reader, binary.BigEndian, &done) != nil || done > 1 ||
		binary.Read(reader, binary.BigEndian, &count) != nil || count == 0 || count > 1<<20 {
		return BackupMessageLogPage{}, channelruntime.ErrInvalidConfig
	}
	page.Boundary.ObservedAtUnixMillis = int64(observedAt)
	page.Done = done == 1
	page.Records = make([][]byte, 0, count)
	for index := uint32(0); index < count; index++ {
		var size uint32
		if binary.Read(reader, binary.BigEndian, &size) != nil || size == 0 || uint64(size) > uint64(reader.Len()) {
			return BackupMessageLogPage{}, channelruntime.ErrInvalidConfig
		}
		start := len(body) - reader.Len()
		record := body[start : start+int(size)]
		if _, err := reader.Seek(int64(size), io.SeekCurrent); err != nil {
			return BackupMessageLogPage{}, channelruntime.ErrInvalidConfig
		}
		page.Records = append(page.Records, record)
	}
	if reader.Len() != 0 {
		return BackupMessageLogPage{}, channelruntime.ErrInvalidConfig
	}
	return page, nil
}

func decodeBackupContinuousMessageJSON(body []byte, target any) error {
	if len(body) == 0 || len(body) > int(MaxCaptureBackupRecordBytes)+1<<20 {
		return channelruntime.ErrInvalidConfig
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return channelruntime.ErrInvalidConfig
	}
	return nil
}
