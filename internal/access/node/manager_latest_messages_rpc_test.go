package node

import (
	"context"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

func TestManagerLatestMessagesRPCReadsLocalPage(t *testing.T) {
	service := &fakeManagerLatestMessageReader{page: managementusecase.ListMessagesResponse{
		Items:   []managementusecase.Message{{MessageID: 101, ChannelID: "room-1", ChannelType: 2}},
		HasMore: true, NextCursor: managementusecase.MessageListCursor{BeforeMessageID: 101},
	}}
	adapter := New(Options{ManagerLatestMessages: service})
	body, err := encodeManagerLatestMessagesRequest(managerLatestMessagesRPCRequest{BeforeMessageID: 110, Limit: 50})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}

	respBody, err := adapter.HandleManagerLatestMessagesRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleManagerLatestMessagesRPC(): %v", err)
	}
	resp, err := decodeManagerLatestMessagesResponse(respBody)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if service.beforeMessageID != 110 || service.limit != 50 || resp.Status != rpcStatusOK || len(resp.Page.Items) != 1 || resp.Page.Items[0].MessageID != 101 {
		t.Fatalf("service/response = before:%d limit:%d resp:%#v", service.beforeMessageID, service.limit, resp)
	}
}

func TestManagerLatestMessagesRPCClientReadsPeer(t *testing.T) {
	service := &fakeManagerLatestMessageReader{page: managementusecase.ListMessagesResponse{
		Items: []managementusecase.Message{{MessageID: 99, ChannelID: "room-2", ChannelType: 1}},
	}}
	adapter := New(Options{ManagerLatestMessages: service})
	node := &fakeManagerConnectionRPCNode{handler: adapter.HandleManagerLatestMessagesRPC}

	page, err := NewClient(node).ListManagerLatestMessages(context.Background(), 2, 100, 20)
	if err != nil {
		t.Fatalf("ListManagerLatestMessages(): %v", err)
	}
	if node.nodeID != 2 || node.serviceID != ManagerLatestMessagesRPCServiceID || len(page.Items) != 1 || page.Items[0].MessageID != 99 {
		t.Fatalf("rpc/page = node:%d service:%d page:%#v", node.nodeID, node.serviceID, page)
	}
}

type fakeManagerLatestMessageReader struct {
	beforeMessageID uint64
	limit           int
	page            managementusecase.ListMessagesResponse
	err             error
}

func (f *fakeManagerLatestMessageReader) ListLocalLatestMessages(_ context.Context, beforeMessageID uint64, limit int) (managementusecase.ListMessagesResponse, error) {
	f.beforeMessageID = beforeMessageID
	f.limit = limit
	return f.page, f.err
}
