package plugin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const defaultHostConversationChannelsLimit = 1000

const defaultHTTPForwardMaxBodyBytes int64 = 10 << 20
const defaultHTTPForwardMaxHeaderBytes int64 = 64 << 10

// SendMessage handles the legacy /message/send host RPC through the message usecase.
func (a *App) SendMessage(ctx context.Context, req *pluginproto.SendReq, _ string) (*pluginproto.SendResp, error) {
	if a == nil || a.messages == nil {
		return nil, ErrMessageSenderRequired
	}
	cmd, err := sendCommandFromPluginReq(req, a.defaultSenderUID)
	if err != nil {
		return nil, err
	}
	result, err := a.messages.Send(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return sendRespFromResult(result), nil
}

// ChannelMessages handles the legacy /channel/messages host RPC through the authoritative reader.
func (a *App) ChannelMessages(ctx context.Context, req *pluginproto.ChannelMessageBatchReq, _ string) (*pluginproto.ChannelMessageBatchResp, error) {
	if a == nil || a.messageReader == nil {
		return nil, ErrMessageReaderRequired
	}
	if req == nil {
		req = &pluginproto.ChannelMessageBatchReq{}
	}
	resp := &pluginproto.ChannelMessageBatchResp{ChannelMessageResps: make([]*pluginproto.ChannelMessageResp, 0, len(req.GetChannelMessageReqs()))}
	for _, item := range req.GetChannelMessageReqs() {
		page, err := a.messageReader.SyncMessages(ctx, channelMessageQueryFromPluginReq(item))
		if errors.Is(err, metadb.ErrNotFound) {
			resp.ChannelMessageResps = append(resp.ChannelMessageResps, channelMessageRespFromPage(item, page))
			continue
		}
		if err != nil {
			return nil, err
		}
		resp.ChannelMessageResps = append(resp.ChannelMessageResps, channelMessageRespFromPage(item, page))
	}
	return resp, nil
}

// ClusterConfig handles the legacy /cluster/config host RPC through an authoritative cluster reader.
func (a *App) ClusterConfig(ctx context.Context, _ string) (*pluginproto.ClusterConfig, error) {
	if a == nil || a.clusterReader == nil {
		return nil, ErrClusterReaderRequired
	}
	snapshot, err := a.clusterReader.ClusterSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	return clusterConfigFromSnapshot(snapshot), nil
}

// ClusterChannelsBelongNode handles legacy channel owner grouping without local fallback guesses.
func (a *App) ClusterChannelsBelongNode(ctx context.Context, req *pluginproto.ClusterChannelBelongNodeReq, _ string) (*pluginproto.ClusterChannelBelongNodeBatchResp, error) {
	if a == nil || a.channelOwners == nil {
		return nil, ErrChannelOwnerReaderRequired
	}
	if req == nil || len(req.GetChannels()) == 0 {
		return nil, ErrChannelRequired
	}
	groups := make(map[uint64][]*pluginproto.Channel)
	for _, item := range req.GetChannels() {
		id, err := channelIDFromPluginChannel(item)
		if err != nil {
			return nil, err
		}
		owner, err := a.channelOwners.ChannelOwnerNode(ctx, id)
		if err != nil {
			return nil, err
		}
		if owner == 0 {
			return nil, fmt.Errorf("%w: %s/%d", ErrChannelOwnerUnknown, id.ID, id.Type)
		}
		groups[owner] = append(groups[owner], clonePluginChannel(item))
	}
	nodeIDs := make([]uint64, 0, len(groups))
	for nodeID := range groups {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })
	resp := &pluginproto.ClusterChannelBelongNodeBatchResp{
		ClusterChannelBelongNodeResps: make([]*pluginproto.ClusterChannelBelongNodeResp, 0, len(nodeIDs)),
	}
	for _, nodeID := range nodeIDs {
		resp.ClusterChannelBelongNodeResps = append(resp.ClusterChannelBelongNodeResps, &pluginproto.ClusterChannelBelongNodeResp{
			NodeId:   nodeID,
			Channels: groups[nodeID],
		})
	}
	return resp, nil
}

// ConversationChannels handles the legacy /conversation/channels host RPC through an authoritative reader.
func (a *App) ConversationChannels(ctx context.Context, req *pluginproto.ConversationChannelReq, _ string) (*pluginproto.ConversationChannelResp, error) {
	if a == nil || a.conversations == nil {
		return nil, ErrConversationReaderRequired
	}
	uid := ""
	if req != nil {
		uid = strings.TrimSpace(req.GetUid())
	}
	if uid == "" {
		return nil, ErrConversationUIDRequired
	}
	channels, err := a.conversations.ConversationChannels(ctx, uid, defaultHostConversationChannelsLimit)
	if err != nil {
		return nil, err
	}
	return conversationChannelsRespFromChannelIDs(channels), nil
}

// HTTPForward handles the legacy /plugin/httpForward host RPC through local routing or node RPC.
func (a *App) HTTPForward(ctx context.Context, req *pluginproto.ForwardHttpReq, callerUID string) (*pluginproto.HttpResponse, error) {
	if req == nil {
		req = &pluginproto.ForwardHttpReq{}
	}
	normalized, err := a.normalizeHTTPForwardRequest(req, callerUID)
	if err != nil {
		return nil, err
	}
	if normalized.GetToNodeId() == -1 {
		return nil, ErrHTTPForwardFanoutDeferred
	}
	if normalized.GetToNodeId() > 0 {
		if a == nil || a.httpForwarder == nil {
			return nil, ErrHTTPForwarderRequired
		}
		return a.httpForwarder.ForwardPluginHTTP(ctx, uint64(normalized.GetToNodeId()), normalized)
	}
	if a == nil {
		return nil, ErrInvokerRequired
	}
	return a.Route(ctx, normalized.GetPluginNo(), normalized.GetRequest())
}

func (a *App) normalizeHTTPForwardRequest(req *pluginproto.ForwardHttpReq, callerUID string) (*pluginproto.ForwardHttpReq, error) {
	pluginNo := strings.TrimSpace(req.GetPluginNo())
	if pluginNo == "" {
		pluginNo = strings.TrimSpace(callerUID)
	}
	if pluginNo == "" {
		return nil, ErrPluginNoRequired
	}
	if err := validatePluginNo(pluginNo); err != nil {
		return nil, err
	}
	httpReq := clonePluginHTTPRequest(req.GetRequest())
	dropHopByHopHeaders(httpReq.Headers)
	if err := a.validateHTTPForwardRequestSize(httpReq); err != nil {
		return nil, err
	}
	return &pluginproto.ForwardHttpReq{
		PluginNo: pluginNo,
		ToNodeId: req.GetToNodeId(),
		Request:  httpReq,
	}, nil
}

func (a *App) validateHTTPForwardRequestSize(req *pluginproto.HttpRequest) error {
	if err := validateHTTPBodySize(req.GetBody(), a.httpForwardBodyLimit()); err != nil {
		return err
	}
	if int64(pluginHTTPHeaderBytes(req.GetHeaders())+pluginHTTPHeaderBytes(req.GetQuery())) > defaultHTTPForwardMaxHeaderBytes {
		return ErrHTTPForwardHeaderTooLarge
	}
	return nil
}

func (a *App) validateHTTPForwardResponseSize(resp *pluginproto.HttpResponse) error {
	if resp == nil {
		return nil
	}
	if err := validateHTTPBodySize(resp.GetBody(), a.httpForwardBodyLimit()); err != nil {
		return err
	}
	if int64(pluginHTTPHeaderBytes(resp.GetHeaders())) > defaultHTTPForwardMaxHeaderBytes {
		return ErrHTTPForwardHeaderTooLarge
	}
	return nil
}

func (a *App) httpForwardBodyLimit() int64 {
	if a != nil && a.httpForwardLimit > 0 {
		return a.httpForwardLimit
	}
	return defaultHTTPForwardMaxBodyBytes
}

func validateHTTPBodySize(body []byte, limit int64) error {
	if int64(len(body)) > limit {
		return fmt.Errorf("%w: %d > %d", ErrHTTPForwardBodyTooLarge, len(body), limit)
	}
	return nil
}

func clonePluginHTTPRequest(req *pluginproto.HttpRequest) *pluginproto.HttpRequest {
	if req == nil {
		req = &pluginproto.HttpRequest{}
	}
	return &pluginproto.HttpRequest{
		Method:  req.GetMethod(),
		Path:    req.GetPath(),
		Headers: cloneStringMap(req.GetHeaders()),
		Query:   cloneStringMap(req.GetQuery()),
		Body:    append([]byte(nil), req.GetBody()...),
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func dropHopByHopHeaders(headers map[string]string) {
	if len(headers) == 0 {
		return
	}
	var connectionTokens []string
	for key, value := range headers {
		if http.CanonicalHeaderKey(key) == "Connection" {
			connectionTokens = append(connectionTokens, strings.Split(value, ",")...)
		}
	}
	for key := range headers {
		if isHopByHopHeader(key) {
			delete(headers, key)
		}
	}
	for _, token := range connectionTokens {
		deleteHeaderCaseInsensitive(headers, strings.TrimSpace(token))
	}
}

func deleteHeaderCaseInsensitive(headers map[string]string, key string) {
	if key == "" {
		return
	}
	canonical := http.CanonicalHeaderKey(key)
	for existing := range headers {
		if http.CanonicalHeaderKey(existing) == canonical {
			delete(headers, existing)
		}
	}
}

func isHopByHopHeader(key string) bool {
	switch http.CanonicalHeaderKey(key) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}

func pluginHTTPHeaderBytes(values map[string]string) int {
	total := 0
	for key, value := range values {
		total += len(key) + len(value)
	}
	return total
}

func channelIDFromPluginChannel(item *pluginproto.Channel) (channel.ChannelID, error) {
	if item == nil || strings.TrimSpace(item.GetChannelId()) == "" {
		return channel.ChannelID{}, ErrChannelRequired
	}
	return channel.ChannelID{ID: item.GetChannelId(), Type: uint8(item.GetChannelType())}, nil
}
