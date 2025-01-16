package ingress

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type TagReq struct {
	TagKey      string
	ChannelId   string
	ChannelType uint8
	NodeId      uint64 // 获取属于指定节点的uids
}

func (t *TagReq) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	enc.WriteString(t.TagKey)
	enc.WriteString(t.ChannelId)
	enc.WriteUint8(t.ChannelType)
	enc.WriteUint64(t.NodeId)
	return enc.Bytes(), nil
}

func (t *TagReq) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if t.TagKey, err = dec.String(); err != nil {
		return err
	}
	if t.ChannelId, err = dec.String(); err != nil {
		return err
	}
	if t.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}
	if t.NodeId, err = dec.Uint64(); err != nil {
		return err
	}
	return nil
}

type TagResp struct {
	TagKey string
	Uids   []string
}

func (t *TagResp) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(t.TagKey)
	enc.WriteUint32(uint32(len(t.Uids)))
	for _, uid := range t.Uids {
		enc.WriteString(uid)
	}
	return enc.Bytes(), nil
}

func (t *TagResp) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	tagKey, err := dec.String()
	if err != nil {
		return err
	}
	t.TagKey = tagKey
	count, err := dec.Uint32()
	if err != nil {
		return err
	}
	t.Uids = make([]string, 0, count)
	for i := 0; i < int(count); i++ {
		var uid string
		if uid, err = dec.String(); err != nil {
			return err
		}
		t.Uids = append(t.Uids, uid)
	}
	return nil
}

type AllowSendReq struct {
	From string // 发送者
	To   string // 接收者
}

func (a *AllowSendReq) decode(data []byte) error {

	dec := wkproto.NewDecoder(data)
	var err error
	if a.From, err = dec.String(); err != nil {
		return err
	}
	if a.To, err = dec.String(); err != nil {
		return err
	}
	return nil
}

func (a *AllowSendReq) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(a.From)
	enc.WriteString(a.To)
	return enc.Bytes(), nil
}

type TagUpdateReq struct {
	TagKey      string
	ChannelId   string
	ChannelType uint8
	Uids        []string
	Remove      bool // 是否是移除uids
	ChannelTag  bool // 是否是频道tag
}

func (t *TagUpdateReq) Encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	enc.WriteString(t.TagKey)
	enc.WriteString(t.ChannelId)
	enc.WriteUint8(t.ChannelType)
	enc.WriteUint32(uint32(len(t.Uids)))
	for _, uid := range t.Uids {
		enc.WriteString(uid)
	}
	enc.WriteUint8(wkutil.BoolToUint8(t.Remove))
	enc.WriteUint8(wkutil.BoolToUint8(t.ChannelTag))
	return enc.Bytes(), nil
}

func (t *TagUpdateReq) Decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if t.TagKey, err = dec.String(); err != nil {
		return err
	}
	if t.ChannelId, err = dec.String(); err != nil {
		return err
	}
	if t.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}
	count, err := dec.Uint32()
	if err != nil {
		return err
	}
	for i := 0; i < int(count); i++ {
		uid, err := dec.String()
		if err != nil {
			return err
		}
		t.Uids = append(t.Uids, uid)
	}
	remove, err := dec.Uint8()
	if err != nil {
		return err
	}
	t.Remove = wkutil.Uint8ToBool(remove)
	channelTag, err := dec.Uint8()
	if err != nil {
		return err
	}
	t.ChannelTag = wkutil.Uint8ToBool(channelTag)
	return nil
}

type TagAddReq struct {
	TagKey string
	Uids   []string
}

func (t *TagAddReq) Encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(t.TagKey)
	enc.WriteUint32(uint32(len(t.Uids)))
	for _, uid := range t.Uids {
		enc.WriteString(uid)
	}
	return enc.Bytes(), nil
}

func (t *TagAddReq) Decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if t.TagKey, err = dec.String(); err != nil {
		return err
	}
	count, err := dec.Uint32()
	if err != nil {
		return err
	}
	for i := 0; i < int(count); i++ {
		uid, err := dec.String()
		if err != nil {
			return err
		}
		t.Uids = append(t.Uids, uid)
	}
	return nil
}

type ChannelReq struct {
	ChannelId   string
	ChannelType uint8
}

func (c *ChannelReq) Encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	enc.WriteString(c.ChannelId)
	enc.WriteUint8(c.ChannelType)
	return enc.Bytes(), nil
}

func (c *ChannelReq) Decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.ChannelId, err = dec.String(); err != nil {
		return err
	}
	if c.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}
	return nil
}

type SubscribersResp struct {
	Subscribers []string
}

func (s *SubscribersResp) Encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(uint32(len(s.Subscribers)))
	for _, uid := range s.Subscribers {
		enc.WriteString(uid)
	}
	return enc.Bytes(), nil
}

func (s *SubscribersResp) Decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	count, err := dec.Uint32()
	if err != nil {
		return err
	}
	s.Subscribers = make([]string, 0, count)
	for i := 0; i < int(count); i++ {
		uid, err := dec.String()
		if err != nil {
			return err
		}
		s.Subscribers = append(s.Subscribers, uid)
	}
	return nil
}
