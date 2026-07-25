package cluster

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	backupContinuousSlotActionObserve     = "observe"
	backupContinuousSlotActionRead        = "read"
	backupContinuousSlotActionChannelMeta = "channel_meta"
	backupContinuousSlotChunkBytes        = 32 << 20
	backupContinuousSlotMaxMetaRecords    = 1024
)

type backupContinuousSlotRPCRequest struct {
	Action      string                          `json:"action"`
	HashSlot    uint16                          `json:"hash_slot,omitempty"`
	Metadata    BackupMetadataLogPageRequest    `json:"metadata,omitempty"`
	After       metadb.ChannelRuntimeMetaCursor `json:"after,omitempty"`
	Limit       int                             `json:"limit,omitempty"`
	ChunkOffset int                             `json:"chunk_offset,omitempty"`
}

type backupContinuousSlotRPCResponse struct {
	Watermark   BackupMetadataHighWatermark     `json:"watermark,omitempty"`
	ChannelMeta []metadb.ChannelRuntimeMeta     `json:"channel_meta,omitempty"`
	Next        metadb.ChannelRuntimeMetaCursor `json:"next,omitempty"`
	Done        bool                            `json:"done,omitempty"`
}

type backupContinuousSlotRPCChunk struct {
	TotalBytes int    `json:"total_bytes"`
	Offset     int    `json:"offset"`
	Data       []byte `json:"data"`
}

type backupContinuousSlotRPCHandler struct {
	node *Node
}

func (h backupContinuousSlotRPCHandler) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	var request backupContinuousSlotRPCRequest
	if err := decodeBackupContinuousMessageJSON(payload, &request); err != nil {
		return nil, err
	}
	if h.node == nil {
		return nil, ErrNotStarted
	}
	response := backupContinuousSlotRPCResponse{}
	switch request.Action {
	case backupContinuousSlotActionObserve:
		watermark, err := h.node.observeBackupMetadataHighWatermarkLocal(ctx, request.HashSlot)
		if err != nil {
			return nil, err
		}
		response.Watermark = watermark
	case backupContinuousSlotActionRead:
		if request.ChunkOffset < 0 {
			return nil, channelruntime.ErrInvalidConfig
		}
		key, err := backupContinuousChunkKey(backupContinuousChunkKindMetadata, request.Metadata)
		if err != nil {
			return nil, err
		}
		totalBytes, data, _, validChunk, err := h.node.backupContinuousChunks.chunk(
			ctx, key, request.ChunkOffset, backupContinuousSlotChunkBytes,
			func(ctx context.Context) ([]byte, error) {
				page, err := h.node.readBackupMetadataLogPageLocal(ctx, request.Metadata)
				if err != nil {
					return nil, err
				}
				return marshalBackupMetadataLogPage(page)
			},
		)
		if err != nil {
			return nil, err
		}
		if !validChunk {
			return nil, channelruntime.ErrInvalidConfig
		}
		responseBody, err := json.Marshal(backupContinuousSlotRPCChunk{
			TotalBytes: totalBytes, Offset: request.ChunkOffset, Data: data,
		})
		return responseBody, err
	case backupContinuousSlotActionChannelMeta:
		if request.Limit <= 0 || request.Limit > backupContinuousSlotMaxMetaRecords {
			return nil, channelruntime.ErrInvalidConfig
		}
		page, next, done, err := h.node.listBackupChannelRuntimeMetaPageLocal(
			ctx, request.HashSlot, request.After, request.Limit,
		)
		if err != nil {
			return nil, err
		}
		response.ChannelMeta, response.Next, response.Done = page, next, done
	default:
		return nil, channelruntime.ErrInvalidConfig
	}
	return json.Marshal(response)
}

func (n *Node) callBackupContinuousSlot(ctx context.Context, nodeID uint64, request backupContinuousSlotRPCRequest) (backupContinuousSlotRPCResponse, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return backupContinuousSlotRPCResponse{}, err
	}
	responseBody, err := n.CallRPC(ctx, nodeID, clusternet.RPCBackupContinuousSlot, body)
	if err != nil {
		return backupContinuousSlotRPCResponse{}, err
	}
	var response backupContinuousSlotRPCResponse
	if err := decodeBackupContinuousMessageJSON(responseBody, &response); err != nil {
		return backupContinuousSlotRPCResponse{}, err
	}
	return response, nil
}

func (n *Node) callBackupContinuousMetadataPage(ctx context.Context, nodeID uint64, request BackupMetadataLogPageRequest) (BackupMetadataLogPage, error) {
	var assembled []byte
	total := 0
	for offset := 0; ; {
		body, err := json.Marshal(backupContinuousSlotRPCRequest{
			Action: backupContinuousSlotActionRead, Metadata: request, ChunkOffset: offset,
		})
		if err != nil {
			return BackupMetadataLogPage{}, err
		}
		responseBody, err := n.CallRPC(ctx, nodeID, clusternet.RPCBackupContinuousSlot, body)
		if err != nil {
			return BackupMetadataLogPage{}, err
		}
		var chunk backupContinuousSlotRPCChunk
		if err := decodeBackupContinuousMessageJSON(responseBody, &chunk); err != nil {
			return BackupMetadataLogPage{}, err
		}
		if chunk.TotalBytes <= 0 || chunk.TotalBytes > int(MaxCaptureBackupRecordBytes)+(1<<20) ||
			chunk.Offset != offset || len(chunk.Data) == 0 ||
			offset > chunk.TotalBytes-len(chunk.Data) {
			return BackupMetadataLogPage{}, channelruntime.ErrInvalidConfig
		}
		if total == 0 {
			total = chunk.TotalBytes
			assembled = make([]byte, 0, total)
		} else if chunk.TotalBytes != total {
			return BackupMetadataLogPage{}, channelruntime.ErrStaleMeta
		}
		assembled = append(assembled, chunk.Data...)
		offset += len(chunk.Data)
		if offset == total {
			return loadBackupMetadataLogPage(assembled)
		}
	}
}

var backupMetadataLogPageMagic = [4]byte{'W', 'K', 'B', 'S'}

func marshalBackupMetadataLogPage(page BackupMetadataLogPage) ([]byte, error) {
	if page.NextIndex == 0 || len(page.Records) > 1<<20 {
		return nil, channelruntime.ErrInvalidConfig
	}
	total := 4 + 2 + 8 + 1 + 4
	for _, record := range page.Records {
		if len(record) == 0 || total > int(MaxCaptureBackupRecordBytes)+(1<<20)-4-len(record) {
			return nil, channelruntime.ErrInvalidConfig
		}
		total += 4 + len(record)
	}
	body := make([]byte, 0, total)
	body = append(body, backupMetadataLogPageMagic[:]...)
	body = binary.BigEndian.AppendUint16(body, 1)
	body = binary.BigEndian.AppendUint64(body, page.NextIndex)
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

func loadBackupMetadataLogPage(body []byte) (BackupMetadataLogPage, error) {
	if len(body) < 4+2+8+1+4 || len(body) > int(MaxCaptureBackupRecordBytes)+(1<<20) ||
		!bytes.Equal(body[:4], backupMetadataLogPageMagic[:]) ||
		binary.BigEndian.Uint16(body[4:6]) != 1 {
		return BackupMetadataLogPage{}, channelruntime.ErrInvalidConfig
	}
	reader := bytes.NewReader(body[6:])
	var page BackupMetadataLogPage
	var done uint8
	var count uint32
	if binary.Read(reader, binary.BigEndian, &page.NextIndex) != nil || page.NextIndex == 0 ||
		binary.Read(reader, binary.BigEndian, &done) != nil || done > 1 ||
		binary.Read(reader, binary.BigEndian, &count) != nil || count > 1<<20 {
		return BackupMetadataLogPage{}, channelruntime.ErrInvalidConfig
	}
	page.Done = done == 1
	page.Records = make([][]byte, 0, count)
	for index := uint32(0); index < count; index++ {
		var size uint32
		if binary.Read(reader, binary.BigEndian, &size) != nil ||
			size == 0 || uint64(size) > uint64(reader.Len()) {
			return BackupMetadataLogPage{}, channelruntime.ErrInvalidConfig
		}
		start := len(body) - reader.Len()
		page.Records = append(page.Records, body[start:start+int(size)])
		if _, err := reader.Seek(int64(size), io.SeekCurrent); err != nil {
			return BackupMetadataLogPage{}, channelruntime.ErrInvalidConfig
		}
	}
	if reader.Len() != 0 {
		return BackupMetadataLogPage{}, channelruntime.ErrInvalidConfig
	}
	return page, nil
}
