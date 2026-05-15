package pluginproto

import "google.golang.org/protobuf/proto"

// 字段类型
type FieldType string

const (
	FieldTypeString FieldType = "string"
	FieldTypeNumber FieldType = "number"
	FieldTypeBool   FieldType = "bool"
	FieldTypeSecret FieldType = "secret"
)

func (f FieldType) String() string {
	return string(f)
}

func (p *PluginInfo) Marshal() ([]byte, error) {
	return proto.Marshal(p)
}

func (p *PluginInfo) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, p)
}

func (s *SendPacket) Marshal() ([]byte, error) {
	return proto.Marshal(s)
}

func (s *SendPacket) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, s)
}

func (m *Message) Marshal() ([]byte, error) {
	return proto.Marshal(m)
}

func (m *Message) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, m)
}

func (m *MessageBatch) Marshal() ([]byte, error) {
	return proto.Marshal(m)
}

func (m *MessageBatch) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, m)
}

func (h *HttpRequest) Marshal() ([]byte, error) {
	return proto.Marshal(h)
}

func (h *HttpRequest) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, h)
}

func (h *HttpResponse) Marshal() ([]byte, error) {
	return proto.Marshal(h)
}

func (h *HttpResponse) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, h)
}

func (c *ChannelMessageReq) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ChannelMessageReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (c *ChannelMessageResp) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ChannelMessageResp) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (c *ChannelMessageBatchReq) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ChannelMessageBatchReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (c *ChannelMessageBatchResp) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ChannelMessageBatchResp) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (c *ClusterConfig) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ClusterConfig) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (c *ClusterChannelBelongNodeReq) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ClusterChannelBelongNodeReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (c *ClusterChannelBelongNodeResp) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ClusterChannelBelongNodeResp) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (c *ClusterChannelBelongNodeBatchResp) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ClusterChannelBelongNodeBatchResp) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}
func (f *ForwardHttpReq) Marshal() ([]byte, error) {
	return proto.Marshal(f)
}

func (f *ForwardHttpReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, f)
}

func (c *ConversationChannelReq) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ConversationChannelReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (c *ConversationChannelResp) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ConversationChannelResp) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (s *StartupResp) Marshal() ([]byte, error) {
	return proto.Marshal(s)
}

func (s *StartupResp) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, s)
}

func (s *Stream) Marshal() ([]byte, error) {
	return proto.Marshal(s)
}

func (s *Stream) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, s)
}

func (s *StreamOpenResp) Marshal() ([]byte, error) {
	return proto.Marshal(s)
}
func (s *StreamOpenResp) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, s)
}

func (s *StreamCloseReq) Marshal() ([]byte, error) {
	return proto.Marshal(s)
}

func (s *StreamCloseReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, s)
}

func (s *StreamWriteReq) Marshal() ([]byte, error) {
	return proto.Marshal(s)
}

func (s *StreamWriteReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, s)
}

func (s *StreamWriteResp) Marshal() ([]byte, error) {
	return proto.Marshal(s)
}

func (s *StreamWriteResp) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, s)
}

func (r *RecvPacket) Marshal() ([]byte, error) {
	return proto.Marshal(r)
}

func (r *RecvPacket) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, r)
}

func (c *ConfigTemplate) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ConfigTemplate) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (s *SendReq) Marshal() ([]byte, error) {
	return proto.Marshal(s)
}

func (s *SendReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, s)
}

func (s *SendResp) Marshal() ([]byte, error) {
	return proto.Marshal(s)
}

func (s *SendResp) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, s)
}
