package pluginproto

import (
	"testing"

	"google.golang.org/protobuf/proto"
)

type pluginWireMessage interface {
	proto.Message
	Marshal() ([]byte, error)
	Unmarshal([]byte) error
}

func TestPluginProtoPublicWrappersRoundTripRemainingMessageShapes(t *testing.T) {
	tests := []struct {
		name  string
		value pluginWireMessage
		empty pluginWireMessage
	}{
		{
			name: "message",
			value: &Message{
				MessageId: 7, MessageSeq: 8, ClientMsgNo: "client-8", StreamNo: "stream-1",
				StreamId: 2, Timestamp: 1_700_000_000, From: "u1", ChannelId: "room",
				ChannelType: 2, Topic: "events", Payload: []byte("payload"),
			},
			empty: &Message{},
		},
		{
			name:  "message batch",
			value: &MessageBatch{Messages: []*Message{{MessageId: 9, MessageSeq: 10, ChannelId: "room", ChannelType: 2}}},
			empty: &MessageBatch{},
		},
		{
			name: "http request",
			value: &HttpRequest{
				Method: "PUT", Path: "/plugins/p1", Headers: map[string]string{"x-id": "1"},
				Query: map[string]string{"dry_run": "true"}, Body: []byte("request"),
			},
			empty: &HttpRequest{},
		},
		{
			name:  "http response",
			value: &HttpResponse{Status: 202, Headers: map[string]string{"content-type": "application/json"}, Body: []byte(`{"ok":true}`)},
			empty: &HttpResponse{},
		},
		{
			name:  "channel message request",
			value: &ChannelMessageReq{ChannelId: "room", ChannelType: 2, StartMessageSeq: 11, Limit: 50},
			empty: &ChannelMessageReq{},
		},
		{
			name: "channel message response",
			value: &ChannelMessageResp{
				ChannelId: "room", ChannelType: 2, StartMessageSeq: 11, Limit: 50,
				Messages: []*Message{{MessageId: 12, MessageSeq: 11, Payload: []byte("message")}},
			},
			empty: &ChannelMessageResp{},
		},
		{
			name: "cluster config",
			value: &ClusterConfig{
				Nodes: []*Node{{Id: 1, ClusterAddr: "node-1:11110", ApiServerAddr: "node-1:5001", Online: true}},
				Slots: []*Slot{{Id: 3, Leader: 1, Term: 4, Replicas: []uint64{1, 2, 3}}},
			},
			empty: &ClusterConfig{},
		},
		{
			name:  "cluster channel request",
			value: &ClusterChannelBelongNodeReq{Channels: []*Channel{{ChannelId: "room", ChannelType: 2}}},
			empty: &ClusterChannelBelongNodeReq{},
		},
		{
			name:  "cluster channel response",
			value: &ClusterChannelBelongNodeResp{NodeId: 2, Channels: []*Channel{{ChannelId: "room", ChannelType: 2}}},
			empty: &ClusterChannelBelongNodeResp{},
		},
		{
			name: "cluster channel batch response",
			value: &ClusterChannelBelongNodeBatchResp{ClusterChannelBelongNodeResps: []*ClusterChannelBelongNodeResp{{
				NodeId: 2, Channels: []*Channel{{ChannelId: "room", ChannelType: 2}},
			}}},
			empty: &ClusterChannelBelongNodeBatchResp{},
		},
		{
			name:  "conversation request",
			value: &ConversationChannelReq{Uid: "u1"},
			empty: &ConversationChannelReq{},
		},
		{
			name:  "conversation response",
			value: &ConversationChannelResp{Channels: []*Channel{{ChannelId: "room", ChannelType: 2}}},
			empty: &ConversationChannelResp{},
		},
		{
			name:  "receive packet",
			value: &RecvPacket{FromUid: "u1", ToUid: "u2", ChannelId: "room", ChannelType: 2, Payload: []byte("recv")},
			empty: &RecvPacket{},
		},
		{
			name:  "config template",
			value: &ConfigTemplate{Fields: []*Field{{Name: "token", Type: FieldTypeSecret.String(), Label: "Token"}}},
			empty: &ConfigTemplate{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := tt.value.Marshal()
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if err := tt.empty.Unmarshal(encoded); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if !proto.Equal(tt.empty, tt.value) {
				t.Fatalf("round trip = %#v, want %#v", tt.empty, tt.value)
			}
			if err := tt.empty.Unmarshal([]byte{0xff}); err == nil {
				t.Fatal("Unmarshal(malformed) error = nil")
			}
		})
	}
}

func TestFieldTypeStringPreservesConfigurationSchemaValues(t *testing.T) {
	for value, want := range map[FieldType]string{
		FieldTypeString: "string",
		FieldTypeNumber: "number",
		FieldTypeBool:   "bool",
		FieldTypeSecret: "secret",
	} {
		if got := value.String(); got != want {
			t.Fatalf("FieldType(%q).String() = %q, want %q", value, got, want)
		}
	}
}
