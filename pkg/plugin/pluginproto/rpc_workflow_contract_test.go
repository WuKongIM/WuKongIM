package pluginproto

import (
	"bytes"
	"reflect"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestPluginStartupContractSurvivesWire(t *testing.T) {
	wantInfo := &PluginInfo{
		No:               "wk.plugin.audit",
		Name:             "Audit",
		Methods:          []string{"Send", "PersistAfter"},
		Version:          "2.4.1",
		Priority:         17,
		PersistAfterSync: true,
		ReplySync:        true,
		ConfigTemplate: &ConfigTemplate{Fields: []*Field{
			{Name: "endpoint", Type: FieldTypeString.String(), Label: "Endpoint"},
			{Name: "token", Type: FieldTypeSecret.String(), Label: "Token"},
		}},
	}
	gotInfo := &PluginInfo{}
	mustPluginWireRoundTrip(t, wantInfo, gotInfo)

	if gotInfo.GetNo() != "wk.plugin.audit" || gotInfo.GetName() != "Audit" || gotInfo.GetVersion() != "2.4.1" {
		t.Fatalf("plugin identity = (%q, %q, %q)", gotInfo.GetNo(), gotInfo.GetName(), gotInfo.GetVersion())
	}
	if !reflect.DeepEqual(gotInfo.GetMethods(), []string{"Send", "PersistAfter"}) {
		t.Fatalf("plugin methods = %#v", gotInfo.GetMethods())
	}
	if gotInfo.GetPriority() != 17 || !gotInfo.GetPersistAfterSync() || !gotInfo.GetReplySync() {
		t.Fatalf("plugin dispatch policy = (%d, %v, %v)", gotInfo.GetPriority(), gotInfo.GetPersistAfterSync(), gotInfo.GetReplySync())
	}
	template := gotInfo.GetConfigTemplate()
	if template == nil || len(template.GetFields()) != 2 {
		t.Fatalf("config template = %#v", template)
	}
	endpoint, token := template.GetFields()[0], template.GetFields()[1]
	if endpoint.GetName() != "endpoint" || endpoint.GetType() != "string" || endpoint.GetLabel() != "Endpoint" {
		t.Fatalf("endpoint field = %#v", endpoint)
	}
	if token.GetName() != "token" || token.GetType() != "secret" || token.GetLabel() != "Token" {
		t.Fatalf("token field = %#v", token)
	}

	wantStartup := &StartupResp{
		NodeId:     ^uint64(0),
		Success:    false,
		ErrMsg:     "configuration rejected",
		SandboxDir: "/var/lib/wukongim/plugins/audit",
		Config:     []byte(`{"endpoint":"https://audit.invalid"}`),
	}
	gotStartup := &StartupResp{}
	mustPluginWireRoundTrip(t, wantStartup, gotStartup)
	if gotStartup.GetNodeId() != ^uint64(0) || gotStartup.GetSuccess() {
		t.Fatalf("startup result = (%d, %v)", gotStartup.GetNodeId(), gotStartup.GetSuccess())
	}
	if gotStartup.GetErrMsg() != "configuration rejected" || gotStartup.GetSandboxDir() != "/var/lib/wukongim/plugins/audit" {
		t.Fatalf("startup diagnostics = (%q, %q)", gotStartup.GetErrMsg(), gotStartup.GetSandboxDir())
	}
	if !bytes.Equal(gotStartup.GetConfig(), wantStartup.Config) {
		t.Fatalf("startup config = %q", gotStartup.GetConfig())
	}
}

func TestPluginMessageHookContractSurvivesWire(t *testing.T) {
	wantSend := &SendPacket{
		FromUid: "alice", ChannelId: "support", ChannelType: ^uint32(0),
		Payload: []byte("hello"), Reason: 7,
		Conn: &Conn{Uid: "alice", ConnId: -1, DeviceId: "ios-1", DeviceFlag: ^uint32(0), DeviceLevel: 2},
	}
	gotSend := &SendPacket{}
	mustPluginWireRoundTrip(t, wantSend, gotSend)
	if gotSend.GetFromUid() != "alice" || gotSend.GetChannelId() != "support" || gotSend.GetChannelType() != ^uint32(0) {
		t.Fatalf("send route = (%q, %q, %d)", gotSend.GetFromUid(), gotSend.GetChannelId(), gotSend.GetChannelType())
	}
	if !bytes.Equal(gotSend.GetPayload(), []byte("hello")) || gotSend.GetReason() != 7 {
		t.Fatalf("send decision = (%q, %d)", gotSend.GetPayload(), gotSend.GetReason())
	}
	conn := gotSend.GetConn()
	if conn == nil || conn.GetUid() != "alice" || conn.GetConnId() != -1 || conn.GetDeviceId() != "ios-1" || conn.GetDeviceFlag() != ^uint32(0) || conn.GetDeviceLevel() != 2 {
		t.Fatalf("send connection = %#v", conn)
	}

	wantRecv := &RecvPacket{FromUid: "alice", ToUid: "bob", ChannelId: "support", ChannelType: 2, Payload: []byte("delivered")}
	gotRecv := &RecvPacket{}
	mustPluginWireRoundTrip(t, wantRecv, gotRecv)
	if gotRecv.GetFromUid() != "alice" || gotRecv.GetToUid() != "bob" || gotRecv.GetChannelId() != "support" || gotRecv.GetChannelType() != 2 || !bytes.Equal(gotRecv.GetPayload(), []byte("delivered")) {
		t.Fatalf("receive packet = %#v", gotRecv)
	}

	wantBatch := &MessageBatch{Messages: []*Message{{
		MessageId: -1, MessageSeq: ^uint64(0), ClientMsgNo: "client-1", StreamNo: "stream-1",
		StreamId: ^uint64(0), Timestamp: ^uint32(0), From: "alice", ChannelId: "support",
		ChannelType: ^uint32(0), Topic: "audit", Payload: []byte("committed"),
	}}}
	gotBatch := &MessageBatch{}
	mustPluginWireRoundTrip(t, wantBatch, gotBatch)
	if len(gotBatch.GetMessages()) != 1 {
		t.Fatalf("message count = %d", len(gotBatch.GetMessages()))
	}
	message := gotBatch.GetMessages()[0]
	if message.GetMessageId() != -1 || message.GetMessageSeq() != ^uint64(0) || message.GetClientMsgNo() != "client-1" || message.GetStreamNo() != "stream-1" || message.GetStreamId() != ^uint64(0) {
		t.Fatalf("message identity = %#v", message)
	}
	if message.GetTimestamp() != ^uint32(0) || message.GetFrom() != "alice" || message.GetChannelId() != "support" || message.GetChannelType() != ^uint32(0) || message.GetTopic() != "audit" || !bytes.Equal(message.GetPayload(), []byte("committed")) {
		t.Fatalf("message delivery fields = %#v", message)
	}
}

func TestPluginForwardHTTPContractSurvivesWire(t *testing.T) {
	wantReq := &ForwardHttpReq{
		PluginNo: "wk.plugin.audit", ToNodeId: -1,
		Request: &HttpRequest{
			Method: "PATCH", Path: "/v1/policy",
			Headers: map[string]string{"content-type": "application/json", "x-request-id": "req-7"},
			Query:   map[string]string{"dry_run": "true"}, Body: []byte(`{"enabled":true}`),
		},
	}
	gotReq := &ForwardHttpReq{}
	mustPluginWireRoundTrip(t, wantReq, gotReq)
	if gotReq.GetPluginNo() != "wk.plugin.audit" || gotReq.GetToNodeId() != -1 {
		t.Fatalf("forward target = (%q, %d)", gotReq.GetPluginNo(), gotReq.GetToNodeId())
	}
	httpReq := gotReq.GetRequest()
	if httpReq == nil || httpReq.GetMethod() != "PATCH" || httpReq.GetPath() != "/v1/policy" {
		t.Fatalf("forward request line = %#v", httpReq)
	}
	if !reflect.DeepEqual(httpReq.GetHeaders(), wantReq.Request.Headers) || !reflect.DeepEqual(httpReq.GetQuery(), wantReq.Request.Query) || !bytes.Equal(httpReq.GetBody(), wantReq.Request.Body) {
		t.Fatalf("forward request metadata = %#v", httpReq)
	}

	wantResp := &HttpResponse{Status: 207, Headers: map[string]string{"x-request-id": "req-7"}, Body: []byte(`{"accepted":true}`)}
	gotResp := &HttpResponse{}
	mustPluginWireRoundTrip(t, wantResp, gotResp)
	if gotResp.GetStatus() != 207 || !reflect.DeepEqual(gotResp.GetHeaders(), wantResp.Headers) || !bytes.Equal(gotResp.GetBody(), wantResp.Body) {
		t.Fatalf("forward response = %#v", gotResp)
	}
}

func TestPluginChannelAndClusterCorrelationSurvivesWire(t *testing.T) {
	wantQueries := &ChannelMessageBatchReq{ChannelMessageReqs: []*ChannelMessageReq{
		{ChannelId: "alpha", ChannelType: 2, StartMessageSeq: 11, Limit: 50},
		{ChannelId: "beta", ChannelType: 3, StartMessageSeq: ^uint64(0), Limit: ^uint32(0)},
	}}
	gotQueries := &ChannelMessageBatchReq{}
	mustPluginWireRoundTrip(t, wantQueries, gotQueries)
	queries := gotQueries.GetChannelMessageReqs()
	if len(queries) != 2 || queries[0].GetChannelId() != "alpha" || queries[0].GetChannelType() != 2 || queries[0].GetStartMessageSeq() != 11 || queries[0].GetLimit() != 50 {
		t.Fatalf("first channel query = %#v", queries)
	}
	if queries[1].GetChannelId() != "beta" || queries[1].GetChannelType() != 3 || queries[1].GetStartMessageSeq() != ^uint64(0) || queries[1].GetLimit() != ^uint32(0) {
		t.Fatalf("second channel query = %#v", queries[1])
	}

	wantPages := &ChannelMessageBatchResp{ChannelMessageResps: []*ChannelMessageResp{
		{ChannelId: "alpha", ChannelType: 2, StartMessageSeq: 11, Limit: 50, Messages: []*Message{{MessageId: 1, MessageSeq: 11}}},
		{ChannelId: "beta", ChannelType: 3, StartMessageSeq: 21, Limit: 25, Messages: []*Message{{MessageId: 2, MessageSeq: 21}}},
	}}
	gotPages := &ChannelMessageBatchResp{}
	mustPluginWireRoundTrip(t, wantPages, gotPages)
	pages := gotPages.GetChannelMessageResps()
	if len(pages) != 2 || pages[0].GetChannelId() != "alpha" || pages[0].GetChannelType() != 2 || pages[0].GetStartMessageSeq() != 11 || pages[0].GetLimit() != 50 || pages[0].GetMessages()[0].GetMessageId() != 1 {
		t.Fatalf("first correlated channel page = %#v", pages)
	}
	if pages[1].GetChannelId() != "beta" || pages[1].GetChannelType() != 3 || pages[1].GetStartMessageSeq() != 21 || pages[1].GetLimit() != 25 || pages[1].GetMessages()[0].GetMessageId() != 2 {
		t.Fatalf("second correlated channel page = %#v", pages[1])
	}

	wantConfig := &ClusterConfig{
		Nodes: []*Node{{Id: 9, ClusterAddr: "node-9:11110", ApiServerAddr: "node-9:5001", Online: true}},
		Slots: []*Slot{{Id: 255, Leader: 9, Term: ^uint32(0), Replicas: []uint64{9, 10, 11}}},
	}
	gotConfig := &ClusterConfig{}
	mustPluginWireRoundTrip(t, wantConfig, gotConfig)
	node, slot := gotConfig.GetNodes()[0], gotConfig.GetSlots()[0]
	if node.GetId() != 9 || node.GetClusterAddr() != "node-9:11110" || node.GetApiServerAddr() != "node-9:5001" || !node.GetOnline() {
		t.Fatalf("cluster node = %#v", node)
	}
	if slot.GetId() != 255 || slot.GetLeader() != 9 || slot.GetTerm() != ^uint32(0) || !reflect.DeepEqual(slot.GetReplicas(), []uint64{9, 10, 11}) {
		t.Fatalf("cluster slot = %#v", slot)
	}

	wantBelongReq := &ClusterChannelBelongNodeReq{Channels: []*Channel{{ChannelId: "alpha", ChannelType: 2}, {ChannelId: "beta", ChannelType: 3}}}
	gotBelongReq := &ClusterChannelBelongNodeReq{}
	mustPluginWireRoundTrip(t, wantBelongReq, gotBelongReq)
	channels := gotBelongReq.GetChannels()
	if len(channels) != 2 || channels[0].GetChannelId() != "alpha" || channels[0].GetChannelType() != 2 || channels[1].GetChannelId() != "beta" || channels[1].GetChannelType() != 3 {
		t.Fatalf("cluster channel request = %#v", channels)
	}

	wantBelong := &ClusterChannelBelongNodeBatchResp{ClusterChannelBelongNodeResps: []*ClusterChannelBelongNodeResp{{NodeId: 9, Channels: wantBelongReq.Channels}}}
	gotBelong := &ClusterChannelBelongNodeBatchResp{}
	mustPluginWireRoundTrip(t, wantBelong, gotBelong)
	groups := gotBelong.GetClusterChannelBelongNodeResps()
	if len(groups) != 1 || groups[0].GetNodeId() != 9 || len(groups[0].GetChannels()) != 2 {
		t.Fatalf("cluster channel response = %#v", groups)
	}

	wantConversationReq := &ConversationChannelReq{Uid: "alice"}
	gotConversationReq := &ConversationChannelReq{}
	mustPluginWireRoundTrip(t, wantConversationReq, gotConversationReq)
	if gotConversationReq.GetUid() != "alice" {
		t.Fatalf("conversation uid = %q", gotConversationReq.GetUid())
	}
	wantConversationResp := &ConversationChannelResp{Channels: wantBelongReq.Channels}
	gotConversationResp := &ConversationChannelResp{}
	mustPluginWireRoundTrip(t, wantConversationResp, gotConversationResp)
	if len(gotConversationResp.GetChannels()) != 2 || gotConversationResp.GetChannels()[1].GetChannelId() != "beta" {
		t.Fatalf("conversation channels = %#v", gotConversationResp.GetChannels())
	}
}

func TestPluginStreamingRequestResponseCorrelationSurvivesWire(t *testing.T) {
	header := &Header{NoPersist: true, RedDot: true, SyncOnce: true}
	wantOpen := &Stream{Header: header, ClientMsgNo: "client-open", FromUid: "alice", ChannelId: "room", ChannelType: 2, Payload: []byte("first")}
	gotOpen := &Stream{}
	mustPluginWireRoundTrip(t, wantOpen, gotOpen)
	if gotOpen.GetHeader() == nil || !gotOpen.GetHeader().GetNoPersist() || !gotOpen.GetHeader().GetRedDot() || !gotOpen.GetHeader().GetSyncOnce() {
		t.Fatalf("stream header = %#v", gotOpen.GetHeader())
	}
	if gotOpen.GetClientMsgNo() != "client-open" || gotOpen.GetFromUid() != "alice" || gotOpen.GetChannelId() != "room" || gotOpen.GetChannelType() != 2 || !bytes.Equal(gotOpen.GetPayload(), []byte("first")) {
		t.Fatalf("stream open request = %#v", gotOpen)
	}

	wantOpenResp := &StreamOpenResp{StreamNo: "stream-42"}
	gotOpenResp := &StreamOpenResp{}
	mustPluginWireRoundTrip(t, wantOpenResp, gotOpenResp)
	if gotOpenResp.GetStreamNo() != "stream-42" {
		t.Fatalf("stream number = %q", gotOpenResp.GetStreamNo())
	}

	wantWrite := &StreamWriteReq{Header: header, StreamNo: "stream-42", ClientMsgNo: "client-write", FromUid: "alice", ChannelId: "room", ChannelType: 2, Payload: []byte("next")}
	gotWrite := &StreamWriteReq{}
	mustPluginWireRoundTrip(t, wantWrite, gotWrite)
	if gotWrite.GetHeader() == nil || gotWrite.GetStreamNo() != "stream-42" || gotWrite.GetClientMsgNo() != "client-write" || gotWrite.GetFromUid() != "alice" || gotWrite.GetChannelId() != "room" || gotWrite.GetChannelType() != 2 || !bytes.Equal(gotWrite.GetPayload(), []byte("next")) {
		t.Fatalf("stream write request = %#v", gotWrite)
	}

	wantWriteResp := &StreamWriteResp{MessageId: -7, ClientMsgNo: "client-write"}
	gotWriteResp := &StreamWriteResp{}
	mustPluginWireRoundTrip(t, wantWriteResp, gotWriteResp)
	if gotWriteResp.GetMessageId() != -7 || gotWriteResp.GetClientMsgNo() != gotWrite.GetClientMsgNo() {
		t.Fatalf("stream write correlation = (%d, %q)", gotWriteResp.GetMessageId(), gotWriteResp.GetClientMsgNo())
	}

	wantClose := &StreamCloseReq{StreamNo: "stream-42", ChannelId: "room", ChannelType: 2}
	gotClose := &StreamCloseReq{}
	mustPluginWireRoundTrip(t, wantClose, gotClose)
	if gotClose.GetStreamNo() != "stream-42" || gotClose.GetChannelId() != "room" || gotClose.GetChannelType() != 2 {
		t.Fatalf("stream close request = %#v", gotClose)
	}

	wantSend := &SendReq{Header: header, ClientMsgNo: "client-send", FromUid: "alice", ChannelId: "room", ChannelType: 2, Payload: []byte("standalone")}
	gotSend := &SendReq{}
	mustPluginWireRoundTrip(t, wantSend, gotSend)
	if gotSend.GetHeader() == nil || gotSend.GetClientMsgNo() != "client-send" || gotSend.GetFromUid() != "alice" || gotSend.GetChannelId() != "room" || gotSend.GetChannelType() != 2 || !bytes.Equal(gotSend.GetPayload(), []byte("standalone")) {
		t.Fatalf("send request = %#v", gotSend)
	}

	wantSendResp := &SendResp{MessageId: -9}
	gotSendResp := &SendResp{}
	mustPluginWireRoundTrip(t, wantSendResp, gotSendResp)
	if gotSendResp.GetMessageId() != -9 {
		t.Fatalf("send response message id = %d", gotSendResp.GetMessageId())
	}
}

func mustPluginWireRoundTrip(t *testing.T, value, empty pluginWireMessage) {
	t.Helper()
	encoded, err := value.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := empty.Unmarshal(encoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !proto.Equal(empty, value) {
		t.Fatalf("round trip = %v, want %v", empty, value)
	}
}
