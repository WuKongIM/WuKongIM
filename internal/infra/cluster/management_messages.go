package cluster

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

// ManagementMessageReader adapts cluster committed message reads to manager message pages.
type ManagementMessageReader struct {
	node   ChannelMessageReadNode
	latest ManagementLatestMessageNode
	remote *accessnode.Client
}

// ManagementLatestMessageNode exposes local storage, membership, and node RPC for global latest reads.
type ManagementLatestMessageNode interface {
	NodeID() uint64
	LocalControlSnapshot(context.Context) (control.Snapshot, error)
	ReadLocalLatestMessages(context.Context, uint64, int) ([]channelruntime.Message, bool, uint64, error)
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// NewManagementMessageReader creates a manager message reader.
func NewManagementMessageReader(node ChannelMessageReadNode) *ManagementMessageReader {
	reader := &ManagementMessageReader{node: node}
	if latest, ok := any(node).(ManagementLatestMessageNode); ok {
		reader.latest = latest
		reader.remote = accessnode.NewClient(latest)
	}
	return reader
}

// ListLocalLatestMessages maps one node-local message index page for node RPC.
func (r *ManagementMessageReader) ListLocalLatestMessages(ctx context.Context, beforeMessageID uint64, limit int) (managementusecase.ListMessagesResponse, error) {
	if r == nil || r.latest == nil || limit <= 0 {
		return managementusecase.ListMessagesResponse{}, managementusecase.ErrLatestMessagesUnavailable
	}
	items, hasMore, next, err := r.latest.ReadLocalLatestMessages(ctx, beforeMessageID, limit)
	if err != nil {
		return managementusecase.ListMessagesResponse{}, err
	}
	resp := managementusecase.ListMessagesResponse{Items: managementMessagesFromChannel(items), HasMore: hasMore}
	if hasMore {
		resp.NextCursor = managementusecase.MessageListCursor{BeforeMessageID: next}
	}
	return resp, nil
}

// QueryLatestMessages fans out node-local index reads and merges replicated rows by message ID.
func (r *ManagementMessageReader) QueryLatestMessages(ctx context.Context, req managementusecase.LatestMessageQueryRequest) (managementusecase.LatestMessageQueryPage, error) {
	if r == nil || r.latest == nil || r.remote == nil || req.Limit <= 0 {
		return managementusecase.LatestMessageQueryPage{}, managementusecase.ErrLatestMessagesUnavailable
	}
	snapshot, err := r.latest.LocalControlSnapshot(ctx)
	if err != nil {
		return managementusecase.LatestMessageQueryPage{}, latestMessagesUnavailable(err)
	}
	nodeIDs := latestMessageDataNodeIDs(snapshot, r.latest.NodeID())
	pages := make([]latestMessageNodePage, 0, len(nodeIDs))
	var pagesMu sync.Mutex
	groupCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	limit := make(chan struct{}, 8)
	var groupWG sync.WaitGroup
	var groupErr error
	var groupErrOnce sync.Once
	setGroupError := func(err error) {
		if err == nil {
			return
		}
		groupErrOnce.Do(func() {
			groupErr = err
			cancel()
		})
	}
launch:
	for _, nodeID := range nodeIDs {
		nodeID := nodeID
		select {
		case limit <- struct{}{}:
		case <-groupCtx.Done():
			setGroupError(groupCtx.Err())
			break launch
		}
		groupWG.Add(1)
		goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskManagerLatestMessagesFanout, func() {
			defer groupWG.Done()
			defer func() { <-limit }()
			var page managementusecase.ListMessagesResponse
			var err error
			if nodeID == r.latest.NodeID() {
				page, err = r.ListLocalLatestMessages(groupCtx, req.BeforeMessageID, req.Limit+1)
			} else {
				page, err = r.remote.ListManagerLatestMessages(groupCtx, nodeID, req.BeforeMessageID, req.Limit+1)
			}
			if err != nil {
				setGroupError(err)
				return
			}
			pagesMu.Lock()
			pages = append(pages, latestMessageNodePage{nodeID: nodeID, items: page.Items})
			pagesMu.Unlock()
		})
	}
	groupWG.Wait()
	if groupErr != nil {
		return managementusecase.LatestMessageQueryPage{}, latestMessagesUnavailable(groupErr)
	}
	return mergeLatestMessagePages(pages, req.Limit)
}

func latestMessagesUnavailable(err error) error {
	if err == nil || errors.Is(err, managementusecase.ErrLatestMessagesUnavailable) {
		return err
	}
	return fmt.Errorf("%w: %v", managementusecase.ErrLatestMessagesUnavailable, err)
}

type latestMessageNodePage struct {
	nodeID uint64
	items  []managementusecase.Message
}

type latestMessageReplica struct {
	nodeID  uint64
	message managementusecase.Message
}

func latestMessageDataNodeIDs(snapshot control.Snapshot, localNodeID uint64) []uint64 {
	nodeIDs := make([]uint64, 0, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if node.NodeID == 0 || node.JoinState == control.NodeJoinStateRemoved || node.JoinState == control.NodeJoinStateJoining || node.Status == control.NodeDown {
			continue
		}
		for _, role := range node.Roles {
			if role == control.RoleData {
				nodeIDs = append(nodeIDs, node.NodeID)
				break
			}
		}
	}
	if len(nodeIDs) == 0 && localNodeID != 0 {
		nodeIDs = append(nodeIDs, localNodeID)
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })
	return nodeIDs
}

func mergeLatestMessagePages(pages []latestMessageNodePage, limit int) (managementusecase.LatestMessageQueryPage, error) {
	replicas := make([]latestMessageReplica, 0, len(pages)*limit)
	for _, page := range pages {
		for _, item := range page.items {
			replicas = append(replicas, latestMessageReplica{nodeID: page.nodeID, message: item})
		}
	}
	sort.Slice(replicas, func(i, j int) bool {
		if replicas[i].message.MessageID != replicas[j].message.MessageID {
			return replicas[i].message.MessageID > replicas[j].message.MessageID
		}
		return replicas[i].nodeID < replicas[j].nodeID
	})
	unique := make([]managementusecase.Message, 0, limit+1)
	byID := make(map[uint64]managementusecase.Message, limit+1)
	for _, replica := range replicas {
		if existing, ok := byID[replica.message.MessageID]; ok {
			if !sameLatestMessage(existing, replica.message) {
				return managementusecase.LatestMessageQueryPage{}, fmt.Errorf("management latest message replica mismatch for message_id %d", replica.message.MessageID)
			}
			continue
		}
		byID[replica.message.MessageID] = replica.message
		unique = append(unique, replica.message)
		if len(unique) == limit+1 {
			break
		}
	}
	page := managementusecase.LatestMessageQueryPage{Items: unique}
	if len(unique) > limit {
		page.HasMore = true
		page.Items = unique[:limit]
		page.NextBeforeMessageID = page.Items[len(page.Items)-1].MessageID
	}
	return page, nil
}

func sameLatestMessage(left, right managementusecase.Message) bool {
	return left.MessageID == right.MessageID && left.MessageSeq == right.MessageSeq &&
		left.ChannelID == right.ChannelID && left.ChannelType == right.ChannelType &&
		left.FromUID == right.FromUID && left.ClientMsgNo == right.ClientMsgNo &&
		left.Timestamp == right.Timestamp && bytes.Equal(left.Payload, right.Payload)
}

// QueryMessages returns one descending committed message page for manager display.
func (r *ManagementMessageReader) QueryMessages(ctx context.Context, req managementusecase.MessageQueryRequest) (managementusecase.MessageQueryPage, error) {
	if r == nil || r.node == nil {
		return managementusecase.MessageQueryPage{}, nil
	}
	read, err := r.node.ReadChannelCommitted(ctx, channelruntime.ChannelID{ID: req.ChannelID, Type: uint8(req.ChannelType)}, managementReadCommittedRequest(req))
	if err != nil {
		return managementusecase.MessageQueryPage{}, mapAppendError(err)
	}
	messages := filterManagementMessages(req, managementMessagesFromChannel(read.Messages))
	hasMore := len(messages) > req.Limit
	if hasMore {
		messages = messages[:req.Limit]
	}
	page := managementusecase.MessageQueryPage{
		Items:   messages,
		HasMore: hasMore,
	}
	if hasMore && len(messages) > 0 {
		page.NextBeforeSeq = messages[len(messages)-1].MessageSeq
	}
	return page, nil
}

// MaxMessageSeqForMeta returns the highest committed message sequence for one runtime metadata row.
func (r *ManagementMessageReader) MaxMessageSeqForMeta(ctx context.Context, meta metadb.ChannelRuntimeMeta) (uint64, error) {
	if r == nil || r.node == nil {
		return 0, nil
	}
	read, err := r.node.ReadChannelCommitted(ctx, channelruntime.ChannelID{ID: meta.ChannelID, Type: uint8(meta.ChannelType)}, channelstore.ReadCommittedRequest{
		FromSeq:  maxUint64(),
		MaxSeq:   maxUint64(),
		Limit:    1,
		MaxBytes: maxInt(),
		Reverse:  true,
	})
	if err != nil {
		return 0, mapAppendError(err)
	}
	if len(read.Messages) == 0 {
		return 0, nil
	}
	return read.Messages[0].MessageSeq, nil
}

func managementReadCommittedRequest(req managementusecase.MessageQueryRequest) channelstore.ReadCommittedRequest {
	fromSeq := req.BeforeSeq
	if fromSeq == 0 {
		fromSeq = maxUint64()
	} else {
		fromSeq--
	}
	return channelstore.ReadCommittedRequest{
		FromSeq:  fromSeq,
		MaxSeq:   maxUint64(),
		Limit:    req.Limit + 1,
		MaxBytes: maxInt(),
		Reverse:  true,
	}
}

func managementMessagesFromChannel(items []channelruntime.Message) []managementusecase.Message {
	out := make([]managementusecase.Message, 0, len(items))
	for _, item := range items {
		out = append(out, managementusecase.Message{
			MessageID:   item.MessageID,
			MessageSeq:  item.MessageSeq,
			ClientMsgNo: item.ClientMsgNo,
			ChannelID:   item.ChannelID,
			ChannelType: int64(item.ChannelType),
			FromUID:     item.FromUID,
			Timestamp:   item.ServerTimestampMS / 1000,
			Payload:     append([]byte(nil), item.Payload...),
		})
	}
	return out
}

func filterManagementMessages(req managementusecase.MessageQueryRequest, items []managementusecase.Message) []managementusecase.Message {
	if req.MessageID == 0 && req.ClientMsgNo == "" {
		return items
	}
	out := items[:0]
	for _, item := range items {
		if req.MessageID != 0 && item.MessageID != req.MessageID {
			continue
		}
		if req.ClientMsgNo != "" && item.ClientMsgNo != req.ClientMsgNo {
			continue
		}
		out = append(out, item)
	}
	return out
}
