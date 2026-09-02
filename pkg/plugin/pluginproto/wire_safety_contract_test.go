package pluginproto

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

func TestPluginProtoPreservesFutureFieldsAcrossRelay(t *testing.T) {
	known, err := (&SendReq{
		ClientMsgNo: "client-7",
		FromUid:     "alice",
		ChannelId:   "room",
		ChannelType: 2,
		Payload:     []byte("hello"),
	}).Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	future := protowire.AppendTag(nil, 100, protowire.VarintType)
	future = protowire.AppendVarint(future, 42)
	future = protowire.AppendTag(future, 101, protowire.BytesType)
	future = protowire.AppendBytes(future, []byte("future-value"))
	wire := append(append([]byte(nil), known...), future...)

	var relay SendReq
	if err := relay.Unmarshal(wire); err != nil {
		t.Fatalf("Unmarshal(future fields) error = %v", err)
	}
	if relay.GetClientMsgNo() != "client-7" || relay.GetChannelId() != "room" {
		t.Fatalf("known fields = (%q, %q)", relay.GetClientMsgNo(), relay.GetChannelId())
	}
	if got := relay.ProtoReflect().GetUnknown(); !bytes.Equal(got, future) {
		t.Fatalf("unknown fields = %#v, want %#v", got, future)
	}

	relay.FromUid = "relay"
	reencoded, err := relay.Marshal()
	if err != nil {
		t.Fatalf("Marshal(relay) error = %v", err)
	}
	var receiver SendReq
	if err := receiver.Unmarshal(reencoded); err != nil {
		t.Fatalf("Unmarshal(relayed) error = %v", err)
	}
	if receiver.GetFromUid() != "relay" || !bytes.Equal(receiver.ProtoReflect().GetUnknown(), future) {
		t.Fatalf("relayed request = %#v, unknown = %#v", &receiver, receiver.ProtoReflect().GetUnknown())
	}
}

func TestPluginProtoDecodeReuseDoesNotLeakPriorRequestState(t *testing.T) {
	reused := &ForwardHttpReq{
		PluginNo: "old-plugin",
		ToNodeId: 99,
		Request: &HttpRequest{
			Method:  "POST",
			Path:    "/old",
			Headers: map[string]string{"authorization": "old-secret"},
			Query:   map[string]string{"old": "true"},
			Body:    []byte("old-body"),
		},
	}
	freshWire, err := (&ForwardHttpReq{PluginNo: "new-plugin"}).Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := reused.Unmarshal(freshWire); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if reused.GetPluginNo() != "new-plugin" || reused.GetToNodeId() != 0 || reused.GetRequest() != nil {
		t.Fatalf("reused request retained stale state: %#v", reused)
	}

	batch := &MessageBatch{Messages: []*Message{{MessageId: 1, Payload: []byte("old")}}}
	if err := batch.Unmarshal(nil); err != nil {
		t.Fatalf("Unmarshal(empty) error = %v", err)
	}
	if len(batch.GetMessages()) != 0 {
		t.Fatalf("reused batch retained %d messages", len(batch.GetMessages()))
	}
}

func TestPluginProtoRejectsMalformedWireClasses(t *testing.T) {
	tests := []struct {
		name string
		wire []byte
		msg  interface{ Unmarshal([]byte) error }
	}{
		{name: "zero field number", wire: []byte{0x00}, msg: &ConversationChannelReq{}},
		{name: "truncated length", wire: []byte{0x0a, 0x05, 'a'}, msg: &ConversationChannelReq{}},
		{name: "invalid utf8", wire: []byte{0x0a, 0x01, 0xff}, msg: &ConversationChannelReq{}},
		{name: "unexpected end group", wire: []byte{0x0c}, msg: &ConversationChannelReq{}},
		{name: "overflowing varint", wire: append([]byte{0x08}, bytes.Repeat([]byte{0x80}, 11)...), msg: &SendResp{}},
		{name: "overflowing length", wire: append([]byte{0x0a}, bytes.Repeat([]byte{0xff}, 11)...), msg: &ConversationChannelReq{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.msg.Unmarshal(tt.wire); err == nil {
				t.Fatal("Unmarshal() error = nil")
			}
		})
	}
}

func TestPluginProtoWireSchemaRemainsCompatible(t *testing.T) {
	const want = `PluginInfo:no=1:string,name=2:string,methods=3:[]string,version=4:string,priority=5:int32,persistAfterSync=6:bool,replySync=7:bool,configTemplate=8:pluginproto.ConfigTemplate
Field:name=1:string,type=2:string,label=3:string
ConfigTemplate:fields=1:[]pluginproto.Field
StartupResp:nodeId=1:uint64,success=2:bool,errMsg=3:string,sandboxDir=4:string,config=5:bytes
Conn:uid=1:string,connId=2:int64,deviceId=3:string,deviceFlag=4:uint32,deviceLevel=5:uint32
SendPacket:fromUid=1:string,channelId=2:string,channelType=3:uint32,payload=4:bytes,reason=5:uint32,conn=6:pluginproto.Conn
RecvPacket:fromUid=1:string,toUid=2:string,channelId=3:string,channelType=4:uint32,payload=5:bytes
Message:messageId=1:int64,messageSeq=2:uint64,clientMsgNo=3:string,streamNo=4:string,streamId=5:uint64,timestamp=6:uint32,from=7:string,channelId=8:string,channelType=9:uint32,topic=10:string,payload=11:bytes
MessageBatch:messages=1:[]pluginproto.Message
HttpRequest:method=1:string,path=2:string,headers=3:map<string,string>,query=4:map<string,string>,body=5:bytes
HttpResponse:status=1:int32,headers=2:map<string,string>,body=3:bytes
ChannelMessageReq:channelId=1:string,channelType=2:uint32,startMessageSeq=3:uint64,limit=4:uint32
ChannelMessageBatchReq:channelMessageReqs=1:[]pluginproto.ChannelMessageReq
ChannelMessageResp:channelId=1:string,channelType=2:uint32,startMessageSeq=3:uint64,limit=4:uint32,messages=5:[]pluginproto.Message
ChannelMessageBatchResp:channelMessageResps=1:[]pluginproto.ChannelMessageResp
ClusterConfig:nodes=1:[]pluginproto.Node,slots=2:[]pluginproto.Slot
Node:id=1:uint64,clusterAddr=2:string,apiServerAddr=3:string,online=4:bool
Slot:id=1:uint32,leader=2:uint64,term=3:uint32,replicas=4:[]uint64
Channel:channelId=1:string,channelType=2:uint32
ClusterChannelBelongNodeReq:channels=1:[]pluginproto.Channel
ClusterChannelBelongNodeResp:nodeId=1:uint64,channels=2:[]pluginproto.Channel
ClusterChannelBelongNodeBatchResp:clusterChannelBelongNodeResps=1:[]pluginproto.ClusterChannelBelongNodeResp
ForwardHttpReq:pluginNo=1:string,toNodeId=2:int64,request=3:pluginproto.HttpRequest
ConversationChannelReq:uid=1:string
ConversationChannelResp:channels=1:[]pluginproto.Channel
Header:noPersist=1:bool,redDot=2:bool,syncOnce=3:bool
Stream:header=1:pluginproto.Header,clientMsgNo=2:string,fromUid=3:string,channelId=4:string,channelType=5:uint32,payload=6:bytes
StreamOpenResp:streamNo=1:string
StreamCloseReq:streamNo=1:string,channelId=2:string,channelType=3:uint32
StreamWriteReq:header=1:pluginproto.Header,streamNo=2:string,clientMsgNo=3:string,fromUid=4:string,channelId=5:string,channelType=6:uint32,payload=7:bytes
StreamWriteResp:messageId=1:int64,clientMsgNo=2:string
SendReq:header=1:pluginproto.Header,clientMsgNo=2:string,fromUid=3:string,channelId=4:string,channelType=5:uint32,payload=6:bytes
SendResp:messageId=1:int64`

	if got := describePluginWireSchema(); got != want {
		t.Fatalf("wire schema changed:\n%s", lineDiff(want, got))
	}
}

func describePluginWireSchema() string {
	messages := File_pkg_plugin_pluginproto_plugin_proto.Messages()
	lines := make([]string, 0, messages.Len())
	for i := 0; i < messages.Len(); i++ {
		message := messages.Get(i)
		fields := make([]string, 0, message.Fields().Len())
		for j := 0; j < message.Fields().Len(); j++ {
			field := message.Fields().Get(j)
			kind := field.Kind().String()
			switch {
			case field.IsMap():
				kind = "map<" + field.MapKey().Kind().String() + "," + field.MapValue().Kind().String() + ">"
			case field.Message() != nil:
				kind = string(field.Message().FullName())
			}
			if field.Cardinality().String() == "repeated" && !field.IsMap() {
				kind = "[]" + kind
			}
			fields = append(fields, string(field.Name())+"="+strconv.Itoa(int(field.Number()))+":"+kind)
		}
		lines = append(lines, string(message.Name())+":"+strings.Join(fields, ","))
	}
	return strings.Join(lines, "\n")
}

func lineDiff(want, got string) string {
	wantLines, gotLines := strings.Split(want, "\n"), strings.Split(got, "\n")
	limit := len(wantLines)
	if len(gotLines) < limit {
		limit = len(gotLines)
	}
	for i := 0; i < limit; i++ {
		if wantLines[i] != gotLines[i] {
			return "want " + wantLines[i] + "\n got " + gotLines[i]
		}
	}
	return "want " + want + "\n got " + got
}
