package node

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/conversationactive"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ConversationAuthorityRPCServiceID is the cluster RPC service for UID conversation authority calls.
const ConversationAuthorityRPCServiceID uint8 = clusternet.RPCConversationAuthority

const (
	conversationBatchUnsupportedTTL        = 5 * time.Minute
	conversationBatchUnsupportedCacheLimit = 1024
)

// ConversationActiveBatchGroup carries one exact authority target and its active-row subset.
type ConversationActiveBatchGroup struct {
	// Target fences only this group's active rows to one observed UID authority epoch.
	Target conversationusecase.RouteTarget
	// Batch carries the already-routed sender and recipient rows for Target.
	Batch conversationactive.ActiveBatch
}

// ConversationActiveBatchResult is aligned with one input bulk admission group.
type ConversationActiveBatchResult struct {
	// Err reports only this exact target group's admission outcome.
	Err error
}

// ConversationBatchAuthority optionally admits multiple exact-target groups in one local call.
type ConversationBatchAuthority interface {
	AdmitActiveBatches(context.Context, []ConversationActiveBatchGroup) []ConversationActiveBatchResult
}

type conversationBatchCapabilityKey struct {
	client *Client
	nodeID uint64
}

var conversationBatchUnsupportedCache = struct {
	sync.Mutex
	entries map[conversationBatchCapabilityKey]time.Time
}{entries: make(map[conversationBatchCapabilityKey]time.Time)}

// HandleConversationAuthorityRPC handles one encoded conversation authority RPC payload.
func (a *Adapter) HandleConversationAuthorityRPC(ctx context.Context, payload []byte) ([]byte, error) {
	if hasMagic(payload, conversationAuthorityBatchRequestMagic[:]) {
		return a.handleConversationActiveBatchGroups(ctx, payload)
	}
	req, err := decodeConversationAuthorityRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("conversation authority rpc decode failed",
			wklog.Event("internal.access.node.conversation_authority_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.conversation == nil {
		return encodeConversationAuthorityResponse(conversationAuthorityResponse{Status: conversationRPCStatusRejected})
	}
	switch req.Op {
	case conversationOpAdmitPatches:
		err = a.conversation.AdmitPatches(ctx, req.Target, req.Patches)
		return encodeConversationAuthorityResponse(conversationAuthorityResponse{Status: conversationRPCStatusForError(err)})
	case conversationOpAdmitActiveBatch:
		err = a.conversation.AdmitActiveBatch(ctx, req.Target, req.ActiveBatch)
		return encodeConversationAuthorityResponse(conversationAuthorityResponse{Status: conversationRPCStatusForError(err)})
	case conversationOpHideConversations:
		err = a.conversation.HideConversationsForTarget(ctx, req.Target, req.Deletes)
		return encodeConversationAuthorityResponse(conversationAuthorityResponse{Status: conversationRPCStatusForError(err)})
	case conversationOpList:
		page, err := a.conversation.ListConversationActiveViewForTarget(ctx, req.Target, req.Kind, req.UID, req.After, req.Limit)
		return encodeConversationAuthorityResponse(conversationAuthorityResponse{Status: conversationRPCStatusForError(err), Page: page})
	case conversationOpDrain:
		result, err := a.conversation.DrainAuthority(ctx, req.Target)
		return encodeConversationAuthorityResponse(conversationAuthorityResponse{Status: conversationRPCStatusForError(err), DrainResult: result})
	default:
		return nil, fmt.Errorf("internal/access/node: unknown conversation authority op %q", req.Op)
	}
}

func (a *Adapter) handleConversationActiveBatchGroups(ctx context.Context, payload []byte) ([]byte, error) {
	groups, err := decodeConversationActiveBatchGroups(payload)
	if err != nil {
		a.rpcLogger().Warn("conversation authority bulk rpc decode failed",
			wklog.Event("internal.access.node.conversation_authority_bulk_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	results := make([]ConversationActiveBatchResult, len(groups))
	if a == nil || a.conversation == nil {
		for i := range results {
			results[i].Err = errors.New("internal/access/node: conversation authority rpc rejected")
		}
	} else if batchAuthority, ok := a.conversation.(ConversationBatchAuthority); ok {
		results = batchAuthority.AdmitActiveBatches(ctx, groups)
		if len(results) != len(groups) {
			return nil, fmt.Errorf("internal/access/node: conversation authority bulk result count %d does not match group count %d", len(results), len(groups))
		}
	} else {
		for i, group := range groups {
			if err := ctx.Err(); err != nil {
				results[i].Err = err
				continue
			}
			results[i].Err = a.conversation.AdmitActiveBatch(ctx, group.Target, group.Batch)
		}
	}
	wireResults := make([]conversationActiveBatchWireResult, len(results))
	for i, result := range results {
		wireResults[i].Status = conversationRPCStatusForError(result.Err)
	}
	return encodeConversationActiveBatchResults(wireResults)
}

// AdmitConversationPatches forwards active conversation patches to nodeID.
func (c *Client) AdmitConversationPatches(ctx context.Context, nodeID uint64, target conversationusecase.RouteTarget, patches []conversationusecase.ActivePatch) error {
	if len(patches) <= maxConversationAuthorityCollectionLen {
		resp, err := c.callConversationAuthority(ctx, nodeID, conversationAuthorityRequest{Op: conversationOpAdmitPatches, Target: target, Kind: metadb.ConversationKindNormal, Patches: patches})
		if err != nil {
			return err
		}
		return conversationRPCErrorForStatus(resp.Status)
	}
	for start := 0; start < len(patches); start += maxConversationAuthorityCollectionLen {
		end := start + maxConversationAuthorityCollectionLen
		if end > len(patches) {
			end = len(patches)
		}
		resp, err := c.callConversationAuthority(ctx, nodeID, conversationAuthorityRequest{
			Op:      conversationOpAdmitPatches,
			Target:  target,
			Kind:    metadb.ConversationKindNormal,
			Patches: patches[start:end],
		})
		if err != nil {
			return err
		}
		if err := conversationRPCErrorForStatus(resp.Status); err != nil {
			return err
		}
	}
	return nil
}

// AdmitConversationActiveBatch forwards one routed active conversation batch to nodeID.
func (c *Client) AdmitConversationActiveBatch(ctx context.Context, nodeID uint64, target conversationusecase.RouteTarget, batch conversationactive.ActiveBatch) error {
	if len(batch.Recipients) == 0 {
		resp, err := c.callConversationAuthority(ctx, nodeID, conversationAuthorityRequest{Op: conversationOpAdmitActiveBatch, Target: target, Kind: metadb.ConversationKindNormal, ActiveBatch: batch})
		if err != nil {
			return err
		}
		return conversationRPCErrorForStatus(resp.Status)
	}
	for start := 0; start < len(batch.Recipients); start += maxConversationAuthorityCollectionLen {
		end := start + maxConversationAuthorityCollectionLen
		if end > len(batch.Recipients) {
			end = len(batch.Recipients)
		}
		chunk := batch
		chunk.Recipients = batch.Recipients[start:end]
		resp, err := c.callConversationAuthority(ctx, nodeID, conversationAuthorityRequest{
			Op:          conversationOpAdmitActiveBatch,
			Target:      target,
			Kind:        metadb.ConversationKindNormal,
			ActiveBatch: chunk,
		})
		if err != nil {
			return err
		}
		if err := conversationRPCErrorForStatus(resp.Status); err != nil {
			return err
		}
	}
	return nil
}

// AdmitConversationActiveBatches forwards multiple exact-target active batches to one destination node.
func (c *Client) AdmitConversationActiveBatches(ctx context.Context, nodeID uint64, groups []ConversationActiveBatchGroup) ([]ConversationActiveBatchResult, error) {
	if len(groups) == 0 {
		return nil, nil
	}
	if nodeID == 0 {
		return nil, fmt.Errorf("internal/access/node: conversation active batch destination node is zero")
	}
	for i, group := range groups {
		if group.Target.LeaderNodeID != nodeID {
			return nil, fmt.Errorf("internal/access/node: conversation active batch group %d leader %d does not match destination %d", i, group.Target.LeaderNodeID, nodeID)
		}
	}
	if c == nil || c.node == nil {
		return nil, fmt.Errorf("internal/access/node: conversation authority rpc client not configured")
	}
	body, err := encodeConversationActiveBatchGroups(groups)
	if err != nil {
		return nil, err
	}
	if c.conversationBatchUnsupported(nodeID, time.Now()) {
		return c.admitConversationActiveBatchesLegacy(ctx, groups), nil
	}
	responseBody, err := c.node.CallRPC(ctx, nodeID, ConversationAuthorityRPCServiceID, body)
	if err != nil {
		if isConversationActiveBatchGroupsUnsupported(err) {
			c.markConversationBatchUnsupported(nodeID, time.Now())
			return c.admitConversationActiveBatchesLegacy(ctx, groups), nil
		}
		return nil, conversationRPCTransportError(err)
	}
	wireResults, err := decodeConversationActiveBatchResults(responseBody)
	if err != nil {
		return nil, err
	}
	if len(wireResults) != len(groups) {
		return nil, fmt.Errorf("internal/access/node: conversation active batch result count %d does not match group count %d", len(wireResults), len(groups))
	}
	results := make([]ConversationActiveBatchResult, len(wireResults))
	for i, wireResult := range wireResults {
		results[i].Err = conversationRPCErrorForStatus(wireResult.Status)
	}
	return results, nil
}

func (c *Client) admitConversationActiveBatchesLegacy(ctx context.Context, groups []ConversationActiveBatchGroup) []ConversationActiveBatchResult {
	results := make([]ConversationActiveBatchResult, len(groups))
	for i, group := range groups {
		if err := ctx.Err(); err != nil {
			results[i].Err = err
			continue
		}
		results[i].Err = c.AdmitConversationActiveBatch(ctx, group.Target.LeaderNodeID, group.Target, group.Batch)
	}
	return results
}

func isConversationActiveBatchGroupsUnsupported(err error) bool {
	if err == nil {
		return false
	}
	var remoteErr transport.RemoteError
	if !errors.As(err, &remoteErr) {
		return false
	}
	return remoteErr.Code == "remote_error" && remoteErr.Message == conversationAuthorityV1InvalidRequestCodecMessage
}

func (c *Client) conversationBatchUnsupported(nodeID uint64, now time.Time) bool {
	key := conversationBatchCapabilityKey{client: c, nodeID: nodeID}
	conversationBatchUnsupportedCache.Lock()
	defer conversationBatchUnsupportedCache.Unlock()
	expiresAt, ok := conversationBatchUnsupportedCache.entries[key]
	if !ok {
		return false
	}
	if !now.Before(expiresAt) {
		delete(conversationBatchUnsupportedCache.entries, key)
		return false
	}
	return true
}

func (c *Client) markConversationBatchUnsupported(nodeID uint64, now time.Time) {
	key := conversationBatchCapabilityKey{client: c, nodeID: nodeID}
	conversationBatchUnsupportedCache.Lock()
	defer conversationBatchUnsupportedCache.Unlock()
	if len(conversationBatchUnsupportedCache.entries) >= conversationBatchUnsupportedCacheLimit {
		var oldestKey conversationBatchCapabilityKey
		var oldestExpiry time.Time
		for entryKey, expiresAt := range conversationBatchUnsupportedCache.entries {
			if !now.Before(expiresAt) {
				delete(conversationBatchUnsupportedCache.entries, entryKey)
				continue
			}
			if oldestExpiry.IsZero() || expiresAt.Before(oldestExpiry) {
				oldestKey = entryKey
				oldestExpiry = expiresAt
			}
		}
		if len(conversationBatchUnsupportedCache.entries) >= conversationBatchUnsupportedCacheLimit && !oldestExpiry.IsZero() {
			delete(conversationBatchUnsupportedCache.entries, oldestKey)
		}
	}
	conversationBatchUnsupportedCache.entries[key] = now.Add(conversationBatchUnsupportedTTL)
}

// HideConversations forwards one bounded exact-target hide collection to nodeID.
func (c *Client) HideConversations(ctx context.Context, nodeID uint64, target conversationusecase.RouteTarget, deletes []metadb.ConversationDelete) error {
	if len(deletes) > maxConversationAuthorityCollectionLen {
		return fmt.Errorf("internal/access/node: conversation deletes length exceeds limit")
	}
	resp, err := c.callConversationAuthority(ctx, nodeID, conversationAuthorityRequest{
		Op:      conversationOpHideConversations,
		Target:  target,
		Kind:    metadb.ConversationKindNormal,
		Deletes: deletes,
	})
	if err != nil {
		return err
	}
	return conversationRPCErrorForStatus(resp.Status)
}

// ListConversations reads the target-owned active conversation page from nodeID.
func (c *Client) ListConversations(ctx context.Context, nodeID uint64, target conversationusecase.RouteTarget, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) (conversationusecase.ActiveViewPage, error) {
	resp, err := c.callConversationAuthority(ctx, nodeID, conversationAuthorityRequest{Op: conversationOpList, Target: target, Kind: kind, UID: uid, After: after, Limit: limit})
	if err != nil {
		return conversationusecase.ActiveViewPage{}, err
	}
	if err := conversationRPCErrorForStatus(resp.Status); err != nil {
		return conversationusecase.ActiveViewPage{}, err
	}
	return resp.Page, nil
}

// DrainConversationAuthority asks nodeID to flush and drain one conversation authority target.
func (c *Client) DrainConversationAuthority(ctx context.Context, nodeID uint64, target conversationusecase.RouteTarget) (string, error) {
	resp, err := c.callConversationAuthority(ctx, nodeID, conversationAuthorityRequest{Op: conversationOpDrain, Target: target, Kind: metadb.ConversationKindNormal})
	if err != nil {
		return "", err
	}
	if err := conversationRPCErrorForStatus(resp.Status); err != nil {
		return "", err
	}
	return resp.DrainResult, nil
}

func (c *Client) callConversationAuthority(ctx context.Context, nodeID uint64, req conversationAuthorityRequest) (conversationAuthorityResponse, error) {
	if c == nil || c.node == nil {
		return conversationAuthorityResponse{}, fmt.Errorf("internal/access/node: conversation authority rpc client not configured")
	}
	body, err := encodeConversationAuthorityRequest(req)
	if err != nil {
		return conversationAuthorityResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, ConversationAuthorityRPCServiceID, body)
	if err != nil {
		return conversationAuthorityResponse{}, conversationRPCTransportError(err)
	}
	return decodeConversationAuthorityResponse(respBody)
}

func conversationRPCTransportError(err error) error {
	if errors.Is(err, transport.ErrCanceled) {
		return context.Canceled
	}
	return err
}

func conversationRPCStatusForError(err error) string {
	switch {
	case err == nil:
		return conversationRPCStatusOK
	case errors.Is(err, conversationusecase.ErrNotLeader):
		return conversationRPCStatusNotLeader
	case errors.Is(err, conversationusecase.ErrStaleRoute):
		return conversationRPCStatusStaleRoute
	case errors.Is(err, conversationusecase.ErrRouteNotReady):
		return conversationRPCStatusRouteNotReady
	case errors.Is(err, conversationusecase.ErrCachePressure):
		return conversationRPCStatusCachePressure
	case errors.Is(err, context.Canceled):
		return conversationRPCStatusCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return conversationRPCStatusDeadline
	default:
		return conversationRPCStatusRejected
	}
}

func conversationRPCErrorForStatus(status string) error {
	switch status {
	case conversationRPCStatusOK:
		return nil
	case conversationRPCStatusNotLeader:
		return conversationusecase.ErrNotLeader
	case conversationRPCStatusStaleRoute:
		return conversationusecase.ErrStaleRoute
	case conversationRPCStatusRouteNotReady:
		return conversationusecase.ErrRouteNotReady
	case conversationRPCStatusCachePressure:
		return conversationusecase.ErrCachePressure
	case conversationRPCStatusCanceled:
		return context.Canceled
	case conversationRPCStatusDeadline:
		return context.DeadlineExceeded
	case conversationRPCStatusRejected:
		return fmt.Errorf("internal/access/node: conversation authority rpc rejected")
	default:
		return fmt.Errorf("internal/access/node: unknown conversation authority rpc status %q", status)
	}
}
