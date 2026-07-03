package cluster

import (
	"container/heap"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	maxPluginBindingScanLimit = 1024

	pluginBindingScanCursorVersion = uint8(1)
	pluginBindingScanRPCVersion    = uint8(1)
)

var pluginBindingScanCursorMagic = [...]byte{'W', 'K', 'P', 'B', 1}

// ListPluginBindingsByPluginNo returns a deterministic plugin-centric binding page.
func (n *Node) ListPluginBindingsByPluginNo(ctx context.Context, pluginNo, cursor string, limit int) ([]metadb.PluginUserBinding, string, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, "", false, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, "", false, err
	}
	if n.defaultSlotMetaDB == nil {
		return nil, "", false, ErrNotStarted
	}
	if limit <= 0 {
		return nil, "", false, metadb.ErrInvalidArgument
	}
	if limit > maxPluginBindingScanLimit {
		limit = maxPluginBindingScanLimit
	}
	after, err := decodePluginBindingScanCursor(cursor)
	if err != nil {
		return nil, "", false, err
	}
	if err := validatePluginBindingScanCursor(pluginNo, after); err != nil {
		return nil, "", false, err
	}
	snapshot := n.Snapshot()
	if snapshot.HashSlotCount == 0 {
		return nil, "", false, ErrRouteNotReady
	}

	queue := make(pluginBindingScanHeap, 0, int(snapshot.HashSlotCount))
	for hashSlot := uint16(0); hashSlot < snapshot.HashSlotCount; hashSlot++ {
		route, err := n.RouteHashSlot(hashSlot)
		if err != nil {
			return nil, "", false, err
		}
		item, ok, err := n.loadPluginBindingScanItem(ctx, route, pluginNo, after)
		if err != nil {
			return nil, "", false, err
		}
		if ok {
			heap.Push(&queue, item)
		}
	}

	out := make([]metadb.PluginUserBinding, 0, limit)
	var last metadb.PluginUserBindingCursor
	for len(out) < limit && queue.Len() > 0 {
		item := heap.Pop(&queue).(pluginBindingScanItem)
		out = append(out, item.Binding)
		last = metadb.PluginUserBindingCursor{PluginNo: item.Binding.PluginNo, UID: item.Binding.UID}
		if item.Done {
			continue
		}
		next, ok, err := n.loadPluginBindingScanItem(ctx, item.Route, pluginNo, item.Cursor)
		if err != nil {
			return nil, "", false, err
		}
		if ok {
			heap.Push(&queue, next)
		}
	}
	if queue.Len() == 0 {
		return out, "", false, nil
	}
	nextCursor, err := encodePluginBindingScanCursor(last)
	if err != nil {
		return nil, "", false, err
	}
	return out, nextCursor, true, nil
}

func (n *Node) loadPluginBindingScanItem(ctx context.Context, route Route, pluginNo string, after metadb.PluginUserBindingCursor) (pluginBindingScanItem, bool, error) {
	bindings, cursor, done, err := n.scanPluginBindingsHashSlot(ctx, route, pluginNo, after, 1)
	if err != nil {
		return pluginBindingScanItem{}, false, err
	}
	if len(bindings) == 0 {
		return pluginBindingScanItem{}, false, nil
	}
	return pluginBindingScanItem{
		Route:    route,
		Binding:  bindings[0],
		Cursor:   cursor,
		Done:     done,
		HashSlot: route.HashSlot,
	}, true, nil
}

func (n *Node) scanPluginBindingsHashSlot(ctx context.Context, route Route, pluginNo string, after metadb.PluginUserBindingCursor, limit int) ([]metadb.PluginUserBinding, metadb.PluginUserBindingCursor, bool, error) {
	if route.Leader == n.cfg.NodeID {
		return n.scanPluginBindingsLocalHashSlot(ctx, route.HashSlot, pluginNo, after, limit)
	}
	body, err := encodePluginBindingScanRPCRequest(pluginBindingScanRPCRequest{
		HashSlot: route.HashSlot,
		PluginNo: pluginNo,
		After:    after,
		Limit:    limit,
	})
	if err != nil {
		return nil, metadb.PluginUserBindingCursor{}, false, err
	}
	respBody, err := n.CallRPC(ctx, route.Leader, clusternet.RPCPluginBindingScan, body)
	if err != nil {
		return nil, metadb.PluginUserBindingCursor{}, false, err
	}
	resp, err := decodePluginBindingScanRPCResponse(respBody)
	if err != nil {
		return nil, metadb.PluginUserBindingCursor{}, false, err
	}
	return append([]metadb.PluginUserBinding(nil), resp.Bindings...), resp.Cursor, resp.Done, nil
}

func (n *Node) scanPluginBindingsLocalHashSlot(ctx context.Context, hashSlot uint16, pluginNo string, after metadb.PluginUserBindingCursor, limit int) ([]metadb.PluginUserBinding, metadb.PluginUserBindingCursor, bool, error) {
	route, err := n.RouteHashSlot(hashSlot)
	if err != nil {
		return nil, metadb.PluginUserBindingCursor{}, false, err
	}
	if route.Leader != n.cfg.NodeID {
		return nil, metadb.PluginUserBindingCursor{}, false, ErrNotLeader
	}
	return n.defaultSlotMetaDB.ForHashSlot(hashSlot).ScanPluginBindingsByPluginNo(ctx, pluginNo, after, limit)
}

type pluginBindingScanHandler struct {
	node *Node
}

func (h pluginBindingScanHandler) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodePluginBindingScanRPCRequest(payload)
	if err != nil {
		return nil, err
	}
	if h.node == nil || h.node.defaultSlotMetaDB == nil {
		return nil, ErrNotStarted
	}
	if err := validatePluginBindingScanCursor(req.PluginNo, req.After); err != nil {
		return nil, err
	}
	if req.Limit <= 0 || req.Limit > maxPluginBindingScanLimit {
		return nil, metadb.ErrInvalidArgument
	}
	bindings, cursor, done, err := h.node.scanPluginBindingsLocalHashSlot(ctx, req.HashSlot, req.PluginNo, req.After, req.Limit)
	if err != nil {
		return nil, err
	}
	return encodePluginBindingScanRPCResponse(pluginBindingScanRPCResponse{
		Bindings: bindings,
		Cursor:   cursor,
		Done:     done,
	})
}

type pluginBindingScanRPCRequest struct {
	Version  uint8                          `json:"version"`
	HashSlot uint16                         `json:"hash_slot"`
	PluginNo string                         `json:"plugin_no"`
	After    metadb.PluginUserBindingCursor `json:"after"`
	Limit    int                            `json:"limit"`
}

type pluginBindingScanRPCResponse struct {
	Version  uint8                          `json:"version"`
	Bindings []metadb.PluginUserBinding     `json:"bindings,omitempty"`
	Cursor   metadb.PluginUserBindingCursor `json:"cursor"`
	Done     bool                           `json:"done"`
}

func encodePluginBindingScanRPCRequest(req pluginBindingScanRPCRequest) ([]byte, error) {
	req.Version = pluginBindingScanRPCVersion
	return json.Marshal(req)
}

func decodePluginBindingScanRPCRequest(payload []byte) (pluginBindingScanRPCRequest, error) {
	var req pluginBindingScanRPCRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return pluginBindingScanRPCRequest{}, err
	}
	if req.Version != pluginBindingScanRPCVersion {
		return pluginBindingScanRPCRequest{}, fmt.Errorf("%w: plugin binding scan rpc version", metadb.ErrInvalidArgument)
	}
	return req, nil
}

func encodePluginBindingScanRPCResponse(resp pluginBindingScanRPCResponse) ([]byte, error) {
	resp.Version = pluginBindingScanRPCVersion
	return json.Marshal(resp)
}

func decodePluginBindingScanRPCResponse(payload []byte) (pluginBindingScanRPCResponse, error) {
	var resp pluginBindingScanRPCResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return pluginBindingScanRPCResponse{}, err
	}
	if resp.Version != pluginBindingScanRPCVersion {
		return pluginBindingScanRPCResponse{}, fmt.Errorf("%w: plugin binding scan rpc version", metadb.ErrInvalidArgument)
	}
	return resp, nil
}

func encodePluginBindingScanCursor(cursor metadb.PluginUserBindingCursor) (string, error) {
	if cursor == (metadb.PluginUserBindingCursor{}) {
		return "", nil
	}
	if err := validatePluginBindingScanCursor(cursor.PluginNo, cursor); err != nil {
		return "", err
	}
	dst := make([]byte, 0, len(pluginBindingScanCursorMagic)+4+len(cursor.PluginNo)+len(cursor.UID))
	dst = append(dst, pluginBindingScanCursorMagic[:]...)
	dst = append(dst, pluginBindingScanCursorVersion)
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(cursor.PluginNo)))
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(cursor.UID)))
	dst = append(dst, cursor.PluginNo...)
	dst = append(dst, cursor.UID...)
	return base64.RawURLEncoding.EncodeToString(dst), nil
}

func decodePluginBindingScanCursor(raw string) (metadb.PluginUserBindingCursor, error) {
	if raw == "" {
		return metadb.PluginUserBindingCursor{}, nil
	}
	body, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return metadb.PluginUserBindingCursor{}, err
	}
	minLen := len(pluginBindingScanCursorMagic) + 1 + 4
	if len(body) < minLen {
		return metadb.PluginUserBindingCursor{}, fmt.Errorf("%w: plugin binding scan cursor", metadb.ErrInvalidArgument)
	}
	if string(body[:len(pluginBindingScanCursorMagic)]) != string(pluginBindingScanCursorMagic[:]) {
		return metadb.PluginUserBindingCursor{}, fmt.Errorf("%w: plugin binding scan cursor magic", metadb.ErrInvalidArgument)
	}
	if body[len(pluginBindingScanCursorMagic)] != pluginBindingScanCursorVersion {
		return metadb.PluginUserBindingCursor{}, fmt.Errorf("%w: plugin binding scan cursor version", metadb.ErrInvalidArgument)
	}
	offset := len(pluginBindingScanCursorMagic) + 1
	pluginLen := int(binary.BigEndian.Uint16(body[offset : offset+2]))
	offset += 2
	uidLen := int(binary.BigEndian.Uint16(body[offset : offset+2]))
	offset += 2
	if len(body)-offset != pluginLen+uidLen {
		return metadb.PluginUserBindingCursor{}, fmt.Errorf("%w: plugin binding scan cursor length", metadb.ErrInvalidArgument)
	}
	pluginNo := string(body[offset : offset+pluginLen])
	offset += pluginLen
	uid := string(body[offset : offset+uidLen])
	cursor := metadb.PluginUserBindingCursor{PluginNo: pluginNo, UID: uid}
	if err := validatePluginBindingScanCursor(pluginNo, cursor); err != nil {
		return metadb.PluginUserBindingCursor{}, err
	}
	return cursor, nil
}

func validatePluginBindingScanCursor(pluginNo string, cursor metadb.PluginUserBindingCursor) error {
	if pluginNo == "" {
		return metadb.ErrInvalidArgument
	}
	if cursor == (metadb.PluginUserBindingCursor{}) {
		return nil
	}
	if cursor.PluginNo != pluginNo || cursor.UID == "" {
		return metadb.ErrInvalidArgument
	}
	return nil
}

type pluginBindingScanItem struct {
	Route    Route
	HashSlot uint16
	Binding  metadb.PluginUserBinding
	Cursor   metadb.PluginUserBindingCursor
	Done     bool
}

type pluginBindingScanHeap []pluginBindingScanItem

func (h pluginBindingScanHeap) Len() int { return len(h) }

func (h pluginBindingScanHeap) Less(i, j int) bool {
	if h[i].Binding.UID != h[j].Binding.UID {
		return h[i].Binding.UID < h[j].Binding.UID
	}
	return h[i].HashSlot < h[j].HashSlot
}

func (h pluginBindingScanHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *pluginBindingScanHeap) Push(x any) {
	*h = append(*h, x.(pluginBindingScanItem))
}

func (h *pluginBindingScanHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
