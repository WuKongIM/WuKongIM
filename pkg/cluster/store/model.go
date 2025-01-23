package store

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type CMDType uint16

const (
	CMDUnknown CMDType = iota
	// 添加设备
	CMDAddDevice
	// 更新设备
	CMDUpdateDevice
	// 添加用户
	CMDAddUser
	// 更新用户
	CMDUpdateUser
	// 添加频道
	CMDAddChannelInfo
	// 更新频道
	CMDUpdateChannelInfo
	// 添加订阅者
	CMDAddSubscribers
	// 移除订阅者
	CMDRemoveSubscribers
	// 移除所有订阅者
	CMDRemoveAllSubscriber
	// 删除频道
	CMDDeleteChannel
	// 添加黑名单
	CMDAddDenylist
	// 移除黑名单
	CMDRemoveDenylist
	// 移除所有黑名单
	CMDRemoveAllDenylist
	// 添加白名单
	CMDAddAllowlist
	// 移除白名单
	CMDRemoveAllowlist
	// 移除所有白名单
	CMDRemoveAllAllowlist
	// 追加消息
	// CMDAppendMessages
	// 追加用户消息
	// CMDAppendMessagesOfUser
	// 追加通知队列消息
	CMDAppendMessagesOfNotifyQueue
	// 移除通知队列消息
	CMDRemoveMessagesOfNotifyQueue
	// 删除频道并清空消息
	CMDDeleteChannelAndClearMessages
	// 添加或更新指定用户的最近会话
	CMDAddOrUpdateUserConversations
	// 删除会话
	CMDDeleteConversation
	// 批量删除会话
	CMDDeleteConversations
	// 添加系统UID
	CMDSystemUIDsAdd
	// 移除系统UID
	CMDSystemUIDsRemove
	// 保存流元数据
	CMDSaveStreamMeta
	// 流结束
	CMDStreamEnd
	// 追加流元素
	CMDAppendStreamItem
	// 频道分布式配置保存
	CMDChannelClusterConfigSave
	// 频道分布式配置删除
	CMDChannelClusterConfigDelete

	// 批量更新最近会话
	CMDBatchUpdateConversation
	// 添加流元数据
	CMDAddStreamMeta
	// 添加流元数据
	CMDAddStreams
	// 批量添加最近会话
	CMDAddOrUpdateConversations
	// 添加或更新测试机
	CMDAddOrUpdateTester
	// 移除测试机
	CMDRemoveTester
)

func (c CMDType) Uint16() uint16 {
	return uint16(c)
}

func (c CMDType) String() string {
	switch c {
	case CMDAddDevice:
		return "CMDAddDevice"
	case CMDUpdateDevice:
		return "CMDUpdateDevice"
	case CMDAddUser:
		return "CMDAddUser"
	case CMDUpdateUser:
		return "CMDUpdateUser"
	case CMDAddChannelInfo:
		return "CMDAddChannelInfo"
	case CMDUpdateChannelInfo:
		return "CMDUpdateChannelInfo"
	case CMDAddSubscribers:
		return "CMDAddSubscribers"
	case CMDRemoveSubscribers:
		return "CMDRemoveSubscribers"
	case CMDRemoveAllSubscriber:
		return "CMDRemoveAllSubscriber"
	case CMDDeleteChannel:
		return "CMDDeleteChannel"
	case CMDAddDenylist:
		return "CMDAddDenylist"
	case CMDRemoveDenylist:
		return "CMDRemoveDenylist"
	case CMDRemoveAllDenylist:
		return "CMDRemoveAllDenylist"
	case CMDAddAllowlist:
		return "CMDAddAllowlist"
	case CMDRemoveAllowlist:
		return "CMDRemoveAllowlist"
	case CMDRemoveAllAllowlist:
		return "CMDRemoveAllAllowlist"
	// case CMDAppendMessages:
	// return "CMDAppendMessages"
	// case CMDAppendMessagesOfUser:
	// return "CMDAppendMessagesOfUser"
	case CMDAppendMessagesOfNotifyQueue:
		return "CMDAppendMessagesOfNotifyQueue"
	case CMDRemoveMessagesOfNotifyQueue:
		return "CMDRemoveMessagesOfNotifyQueue"
	case CMDDeleteChannelAndClearMessages:
		return "CMDDeleteChannelAndClearMessages"
	case CMDAddOrUpdateUserConversations:
		return "CMDAddOrUpdateUserConversations"
	case CMDDeleteConversation:
		return "CMDDeleteConversation"
	case CMDSystemUIDsAdd:
		return "CMDSystemUIDsAdd"
	case CMDSystemUIDsRemove:
		return "CMDSystemUIDsRemove"
	case CMDSaveStreamMeta:
		return "CMDSaveStreamMeta"
	case CMDStreamEnd:
		return "CMDStreamEnd"
	case CMDAppendStreamItem:
		return "CMDAppendStreamItem"
	case CMDChannelClusterConfigSave:
		return "CMDChannelClusterConfigSave"
	case CMDChannelClusterConfigDelete:
		return "CMDChannelClusterConfigDelete"
	case CMDBatchUpdateConversation:
		return "CMDBatchUpdateConversation"
	case CMDDeleteConversations:
		return "CMDDeleteConversations"
	case CMDAddStreamMeta:
		return "CMDAddStreamMeta"
	case CMDAddStreams:
		return "CMDAddStreams"
	case CMDAddOrUpdateConversations:
		return "CMDAddOrUpdateConversations"
	case CMDAddOrUpdateTester:
		return "CMDAddOrUpdateTester"
	case CMDRemoveTester:
		return "CMDRemoveTester"
	default:
		return fmt.Sprintf("CMDUnknown[%d]", c)
	}
}

type CMD struct {
	CmdType CMDType
	Data    []byte
	version CmdVersion // 数据协议版本

}

func NewCMD(cmdType CMDType, data []byte) *CMD {
	return &CMD{
		CmdType: cmdType,
		Data:    data,
	}
}

func NewCMDWithVersion(cmdType CMDType, data []byte, version CmdVersion) *CMD {
	return &CMD{
		CmdType: cmdType,
		Data:    data,
		version: version,
	}
}

func (c *CMD) Marshal() ([]byte, error) {
	c.version = 1
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(c.version.Uint16())
	enc.WriteUint16(c.CmdType.Uint16())
	enc.WriteBytes(c.Data)
	return enc.Bytes(), nil

}

func (c *CMD) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	var version uint16
	if version, err = dec.Uint16(); err != nil {
		return err
	}
	c.version = CmdVersion(version)
	var cmdType uint16
	if cmdType, err = dec.Uint16(); err != nil {
		return err
	}
	c.CmdType = CMDType(cmdType)
	if c.Data, err = dec.BinaryAll(); err != nil {
		return err
	}
	return nil
}

func (c *CMD) CMDContent() (string, error) {
	switch c.CmdType {
	case CMDAddDevice:
		device, err := c.DecodeCMDDevice()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(device), nil
	case CMDUpdateDevice:
		device, err := c.DecodeCMDDevice()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(device), nil
	case CMDAddUser:
		user, err := c.DecodeCMDUser()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(user), nil
	case CMDUpdateUser:
		user, err := c.DecodeCMDUser()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(user), nil
	case CMDAddChannelInfo:
		channel, err := c.DecodeChannelInfo()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(channel), nil
	case CMDUpdateChannelInfo:
		channel, err := c.DecodeChannelInfo()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(channel), nil
	case CMDAddSubscribers:
		channelId, channelType, members, err := c.DecodeMembers()
		if err != nil {
			return "", err
		}
		uids := make([]string, 0, len(members))
		for _, member := range members {
			uids = append(uids, member.Uid)
		}
		return wkutil.ToJSON(map[string]interface{}{
			"channelId":   channelId,
			"channelType": channelType,
			"uids":        uids,
		}), nil
	case CMDRemoveSubscribers:
		channelId, channelType, uids, err := c.DecodeChannelUids()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(map[string]interface{}{
			"channelId":   channelId,
			"channelType": channelType,
			"uids":        uids,
		}), nil
	case CMDRemoveAllSubscriber:
		channelId, channelType, err := c.DecodeChannel()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(map[string]interface{}{
			"channelId":   channelId,
			"channelType": channelType,
		}), nil

	case CMDDeleteChannel:
		channelId, channelType, err := c.DecodeChannel()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(map[string]interface{}{
			"channelId":   channelId,
			"channelType": channelType,
		}), nil
	case CMDAddDenylist:
		channelId, channelType, members, err := c.DecodeMembers()
		if err != nil {
			return "", err
		}
		uids := make([]string, 0, len(members))
		for _, member := range members {
			uids = append(uids, member.Uid)
		}
		return wkutil.ToJSON(map[string]interface{}{
			"channelId":   channelId,
			"channelType": channelType,
			"uids":        uids,
		}), nil

	case CMDRemoveDenylist:
		channelId, channelType, uids, err := c.DecodeChannelUids()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(map[string]interface{}{
			"channelId":   channelId,
			"channelType": channelType,
			"uids":        uids,
		}), nil

	case CMDRemoveAllDenylist:
		channelId, channelType, err := c.DecodeChannel()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(map[string]interface{}{
			"channelId":   channelId,
			"channelType": channelType,
		}), nil

	case CMDAddAllowlist:
		channelId, channelType, members, err := c.DecodeMembers()
		if err != nil {
			return "", err
		}
		uids := make([]string, 0, len(members))
		for _, member := range members {
			uids = append(uids, member.Uid)
		}
		return wkutil.ToJSON(map[string]interface{}{
			"channelId":   channelId,
			"channelType": channelType,
			"uids":        uids,
		}), nil

	case CMDRemoveAllowlist:
		channelId, channelType, uids, err := c.DecodeChannelUids()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(map[string]interface{}{
			"channelId":   channelId,
			"channelType": channelType,
			"uids":        uids,
		}), nil

	case CMDRemoveAllAllowlist:
		channelId, channelType, err := c.DecodeChannel()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(map[string]interface{}{
			"channelId":   channelId,
			"channelType": channelType,
		}), nil

	case CMDDeleteChannelAndClearMessages:
		channelId, channelType, err := c.DecodeChannel()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(map[string]interface{}{
			"channelId":   channelId,
			"channelType": channelType,
		}), nil

	case CMDAddOrUpdateUserConversations:
		uid, conversations, err := c.DecodeCMDAddOrUpdateUserConversations()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(map[string]interface{}{
			"uid":           uid,
			"conversations": conversations,
		}), nil

	case CMDDeleteConversation:
		uid, channelId, channelType, err := c.DecodeCMDDeleteConversation()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(map[string]interface{}{
			"uid":         uid,
			"channelId":   channelId,
			"channelType": channelType,
		}), nil

	case CMDDeleteConversations:
		uid, channels, err := c.DecodeCMDDeleteConversations()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(map[string]interface{}{
			"uid":      uid,
			"channels": channels,
		}), nil

	case CMDSystemUIDsAdd:
		uids, err := c.DecodeCMDSystemUIDs()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(uids), nil

	case CMDSystemUIDsRemove:
		uids, err := c.DecodeCMDSystemUIDs()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(uids), nil

	case CMDBatchUpdateConversation:
		models, err := c.DecodeCMDBatchUpdateConversation()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(models), nil

	case CMDChannelClusterConfigSave:
		_, _, data, err := c.DecodeCMDChannelClusterConfigSave()
		if err != nil {
			return "", err
		}
		channelClusterConfig := wkdb.ChannelClusterConfig{}
		err = channelClusterConfig.Unmarshal(data)
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(channelClusterConfig), nil
	case CMDAddOrUpdateConversations:
		conversations, err := c.DecodeCMDAddOrUpdateConversations()
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(conversations), nil

	}

	return "", nil
}

func EncodeMembers(channelId string, channelType uint8, members []wkdb.Member) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelId)
	encoder.WriteUint8(channelType)
	encoder.WriteUint32(uint32(len(members)))
	if len(members) > 0 {
		for _, member := range members {
			memberData, err := member.Marshal()
			if err != nil {
				return nil
			}
			encoder.WriteBinary(memberData)
		}
	}
	return encoder.Bytes()
}

func (c *CMD) DecodeChannelUids() (channelId string, channelType uint8, uids []string, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if channelId, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	var count uint32
	if count, err = decoder.Uint32(); err != nil {
		return
	}
	for i := uint32(0); i < count; i++ {
		var uid string
		if uid, err = decoder.String(); err != nil {
			return
		}
		uids = append(uids, uid)
	}
	return
}

func EncodeChannelUids(channelId string, channelType uint8, uids []string) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelId)
	encoder.WriteUint8(channelType)
	encoder.WriteUint32(uint32(len(uids)))
	for _, uid := range uids {
		encoder.WriteString(uid)
	}
	return encoder.Bytes()
}

// DecodeMembers DecodeMembers
func (c *CMD) DecodeMembers() (channelId string, channelType uint8, members []wkdb.Member, err error) {
	decoder := wkproto.NewDecoder(c.Data)

	if channelId, err = decoder.String(); err != nil {
		return
	}

	if channelType, err = decoder.Uint8(); err != nil {
		return
	}

	var count uint32
	if count, err = decoder.Uint32(); err != nil {
		return
	}
	if count > 0 {
		for i := uint32(0); i < count; i++ {
			var memberBytes []byte
			if memberBytes, err = decoder.Binary(); err != nil {
				return
			}
			var member = &wkdb.Member{}
			err = member.Unmarshal(memberBytes)
			if err != nil {
				return
			}
			members = append(members, *member)
		}
	}
	return
}

func (c *CMD) DecodeChannel() (channelId string, channelType uint8, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if channelId, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	return
}

func EncodeChannel(channelId string, channelType uint8) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelId)
	encoder.WriteUint8(channelType)
	return encoder.Bytes()
}

func EncodeCMDUser(u wkdb.User) []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(u.Id)
	enc.WriteString(u.Uid)
	if u.CreatedAt != nil {
		enc.WriteUint64(uint64(u.CreatedAt.UnixNano()))
	} else {
		enc.WriteUint64(0)
	}

	if u.UpdatedAt != nil {
		enc.WriteUint64(uint64(u.UpdatedAt.UnixNano()))
	} else {
		enc.WriteUint64(0)
	}
	return enc.Bytes()
}

func (c *CMD) DecodeCMDUser() (u wkdb.User, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if u.Id, err = decoder.Uint64(); err != nil {
		return
	}
	if u.Uid, err = decoder.String(); err != nil {
		return
	}

	var createdAtUnixNano uint64
	if createdAtUnixNano, err = decoder.Uint64(); err != nil {
		return
	}
	if createdAtUnixNano > 0 {
		ct := time.Unix(int64(createdAtUnixNano/1e9), int64(createdAtUnixNano%1e9))
		u.CreatedAt = &ct
	}

	var updatedAtUnixNano uint64
	if updatedAtUnixNano, err = decoder.Uint64(); err != nil {
		return
	}
	if updatedAtUnixNano > 0 {
		ct := time.Unix(int64(updatedAtUnixNano/1e9), int64(updatedAtUnixNano%1e9))
		u.UpdatedAt = &ct
	}

	return
}

// EncodeCMDDevice EncodeCMDDevice
func EncodeCMDDevice(d wkdb.Device) []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(d.Id)
	enc.WriteString(d.Uid)
	enc.WriteUint64(d.DeviceFlag)
	enc.WriteUint8(d.DeviceLevel)
	enc.WriteString(d.Token)
	if d.CreatedAt != nil {
		enc.WriteUint64(uint64(d.CreatedAt.UnixNano()))
	} else {
		enc.WriteUint64(0)
	}

	if d.UpdatedAt != nil {
		enc.WriteUint64(uint64(d.UpdatedAt.UnixNano()))
	} else {
		enc.WriteUint64(0)
	}

	return enc.Bytes()
}

func (c *CMD) DecodeCMDDevice() (d wkdb.Device, err error) {
	decoder := wkproto.NewDecoder(c.Data)

	if d.Id, err = decoder.Uint64(); err != nil {
		return
	}

	if d.Uid, err = decoder.String(); err != nil {
		return
	}
	if d.DeviceFlag, err = decoder.Uint64(); err != nil {
		return
	}

	if d.DeviceLevel, err = decoder.Uint8(); err != nil {
		return
	}
	if d.Token, err = decoder.String(); err != nil {
		return
	}

	var createdAtUnixNano uint64
	if createdAtUnixNano, err = decoder.Uint64(); err != nil {
		return
	}
	if createdAtUnixNano > 0 {
		ct := time.Unix(int64(createdAtUnixNano/1e9), int64(createdAtUnixNano%1e9))
		d.CreatedAt = &ct
	}

	var updatedAtUnixNano uint64
	if updatedAtUnixNano, err = decoder.Uint64(); err != nil {
		return
	}
	if updatedAtUnixNano > 0 {
		ct := time.Unix(int64(updatedAtUnixNano/1e9), int64(updatedAtUnixNano%1e9))
		d.UpdatedAt = &ct
	}

	return
}

func EncodeCMDUserAndDevice(id uint64, uid string, deviceFlag wkproto.DeviceFlag, deviceLevel wkproto.DeviceLevel, token string, createdAt *time.Time, updatedAt *time.Time) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteUint64(id)
	encoder.WriteString(uid)
	encoder.WriteUint64(uint64(deviceFlag))
	encoder.WriteUint8(uint8(deviceLevel))
	encoder.WriteString(token)
	if createdAt != nil {
		encoder.WriteUint64(uint64(createdAt.UnixNano()))
	} else {
		encoder.WriteUint64(0)
	}

	if updatedAt != nil {
		encoder.WriteUint64(uint64(updatedAt.UnixNano()))
	} else {
		encoder.WriteUint64(0)
	}

	return encoder.Bytes()
}

func (c *CMD) DecodeCMDUserAndDevice() (id uint64, uid string, deviceFlag uint64, deviceLevel wkproto.DeviceLevel, token string, createdAt *time.Time, updatedAt *time.Time, err error) {
	decoder := wkproto.NewDecoder(c.Data)

	if id, err = decoder.Uint64(); err != nil {
		return
	}

	if uid, err = decoder.String(); err != nil {
		return
	}
	if deviceFlag, err = decoder.Uint64(); err != nil {
		return
	}
	var deviceLevelUint8 uint8
	if deviceLevelUint8, err = decoder.Uint8(); err != nil {
		return
	}
	deviceLevel = wkproto.DeviceLevel(deviceLevelUint8)

	if token, err = decoder.String(); err != nil {
		return
	}

	var createdAtUnixNano uint64
	if createdAtUnixNano, err = decoder.Uint64(); err != nil {
		return
	}
	if createdAtUnixNano > 0 {
		ct := time.Unix(int64(createdAtUnixNano/1e9), int64(createdAtUnixNano%1e9))
		createdAt = &ct
	}

	var updatedAtUnixNano uint64
	if updatedAtUnixNano, err = decoder.Uint64(); err != nil {
		return
	}
	if updatedAtUnixNano > 0 {
		ct := time.Unix(int64(updatedAtUnixNano/1e9), int64(updatedAtUnixNano%1e9))
		updatedAt = &ct
	}

	return
}

// EncodeCMDUpdateMessageOfUserCursorIfNeed EncodeCMDUpdateMessageOfUserCursorIfNeed
func EncodeCMDUpdateMessageOfUserCursorIfNeed(uid string, messageSeq uint64) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(uid)
	encoder.WriteUint64(messageSeq)
	return encoder.Bytes()
}

// DecodeCMDUpdateMessageOfUserCursorIfNeed DecodeCMDUpdateMessageOfUserCursorIfNeed
func (c *CMD) DecodeCMDUpdateMessageOfUserCursorIfNeed() (uid string, messageSeq uint64, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if uid, err = decoder.String(); err != nil {
		return
	}
	if messageSeq, err = decoder.Uint64(); err != nil {
		return
	}
	return
}

// EncodeChannelInfo EncodeChannelInfo
func EncodeChannelInfo(c wkdb.ChannelInfo, version CmdVersion) ([]byte, error) {

	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(c.ChannelId)
	enc.WriteUint8(c.ChannelType)
	enc.WriteUint8(wkutil.BoolToUint8(c.Ban))
	enc.WriteUint8(wkutil.BoolToUint8(c.Large))
	enc.WriteUint8(wkutil.BoolToUint8(c.Disband))
	if c.CreatedAt != nil {
		enc.WriteUint64(uint64(c.CreatedAt.UnixNano()))
	} else {
		enc.WriteUint64(0)
	}
	if c.UpdatedAt != nil {
		enc.WriteUint64(uint64(c.UpdatedAt.UnixNano()))
	} else {
		enc.WriteUint64(0)
	}
	if version > 0 {
		enc.WriteString(c.Webhook)
	}
	return enc.Bytes(), nil
}

// DecodeChannelInfo DecodeChannelInfo
func (c *CMD) DecodeChannelInfo() (wkdb.ChannelInfo, error) {
	channelInfo := wkdb.ChannelInfo{}
	dec := wkproto.NewDecoder(c.Data)

	var err error
	if channelInfo.ChannelId, err = dec.String(); err != nil {
		return channelInfo, err
	}
	if channelInfo.ChannelType, err = dec.Uint8(); err != nil {
		return channelInfo, err
	}
	var ban uint8
	if ban, err = dec.Uint8(); err != nil {
		return channelInfo, err
	}
	var large uint8
	if large, err = dec.Uint8(); err != nil {
		return channelInfo, err
	}
	var disband uint8
	if disband, err = dec.Uint8(); err != nil {
		return channelInfo, err
	}

	channelInfo.Ban = wkutil.Uint8ToBool(ban)
	channelInfo.Large = wkutil.Uint8ToBool(large)
	channelInfo.Disband = wkutil.Uint8ToBool(disband)

	var createdAt uint64
	if createdAt, err = dec.Uint64(); err != nil {
		return channelInfo, err
	}
	if createdAt > 0 {
		ct := time.Unix(int64(createdAt/1e9), int64(createdAt%1e9))
		channelInfo.CreatedAt = &ct
	}
	var updatedAt uint64
	if updatedAt, err = dec.Uint64(); err != nil {
		return channelInfo, err
	}
	if updatedAt > 0 {
		ct := time.Unix(int64(updatedAt/1e9), int64(updatedAt%1e9))
		channelInfo.UpdatedAt = &ct
	}

	if c.version > 0 {
		if channelInfo.Webhook, err = dec.String(); err != nil {
			return channelInfo, err
		}
	}

	return channelInfo, err
}

// EncodeCMDAddOrUpdateUserConversations EncodeCMDAddOrUpdateConversations
func EncodeCMDAddOrUpdateUserConversations(uid string, conversations []wkdb.Conversation) ([]byte, error) {

	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(uid)
	encoder.WriteUint32(uint32(len(conversations)))
	for _, conversation := range conversations {
		data, err := conversation.Marshal()
		if err != nil {
			return nil, err
		}
		encoder.WriteBinary(data)
	}
	return encoder.Bytes(), nil
}

// DecodeCMDAddOrUpdateUserConversations
func (c *CMD) DecodeCMDAddOrUpdateUserConversations() (uid string, conversations []wkdb.Conversation, err error) {
	if len(c.Data) == 0 {
		return
	}
	decoder := wkproto.NewDecoder(c.Data)
	if uid, err = decoder.String(); err != nil {
		return
	}

	var count uint32
	if count, err = decoder.Uint32(); err != nil {
		return
	}
	for i := uint32(0); i < count; i++ {
		var conversationBytes []byte
		if conversationBytes, err = decoder.Binary(); err != nil {
			return
		}
		var conversation = &wkdb.Conversation{}
		err = conversation.Unmarshal(conversationBytes)
		if err != nil {
			return
		}
		conversations = append(conversations, *conversation)

	}

	return
}

func EncodeCMDDeleteConversation(uid string, channelId string, channelType uint8) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(uid)
	encoder.WriteString(channelId)
	encoder.WriteUint8(channelType)
	return encoder.Bytes()
}

func (c *CMD) DecodeCMDDeleteConversation() (uid string, channelId string, channelType uint8, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if uid, err = decoder.String(); err != nil {
		return
	}
	if channelId, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	return
}

func EncodeCMDDeleteConversations(uid string, channels []wkdb.Channel) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(uid)
	encoder.WriteInt32(int32(len(channels)))
	for _, channel := range channels {
		encoder.WriteString(channel.ChannelId)
		encoder.WriteUint8(channel.ChannelType)
	}
	return encoder.Bytes()
}

func (c *CMD) DecodeCMDDeleteConversations() (uid string, channels []wkdb.Channel, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if uid, err = decoder.String(); err != nil {
		return
	}
	var count int32
	count, err = decoder.Int32()
	if err != nil {
		return
	}

	var channelId string
	var channelType uint8
	for i := 0; i < int(count); i++ {
		channelId, err = decoder.String()
		if err != nil {
			return
		}
		channelType, err = decoder.Uint8()
		if err != nil {
			return
		}
		channels = append(channels, wkdb.Channel{
			ChannelId:   channelId,
			ChannelType: channelType,
		})

	}
	return

}

func EncodeCMDStreamEnd(channelID string, channelType uint8, streamNo string) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelID)
	encoder.WriteUint8(channelType)
	encoder.WriteString(streamNo)
	return encoder.Bytes()
}

func (c *CMD) DecodeCMDStreamEnd() (channelID string, channelType uint8, streamNo string, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if channelID, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	if streamNo, err = decoder.String(); err != nil {
		return
	}
	return
}

// func EncodeCMDAppendStreamItem(channelID string, channelType uint8, streamNo string, item *wkstore.StreamItem) []byte {
// 	encoder := wkproto.NewEncoder()
// 	defer encoder.End()

// 	encoder.WriteString(channelID)
// 	encoder.WriteUint8(channelType)
// 	encoder.WriteString(streamNo)
// 	encoder.WriteBinary(wkstore.EncodeStreamItem(item))

// 	return encoder.Bytes()
// }
// func (c *CMD) DecodeCMDAppendStreamItem() (channelID string, channelType uint8, streamNo string, item *wkstore.StreamItem, err error) {
// 	decoder := wkproto.NewDecoder(c.Data)
// 	if channelID, err = decoder.String(); err != nil {
// 		return
// 	}
// 	if channelType, err = decoder.Uint8(); err != nil {
// 		return
// 	}
// 	if streamNo, err = decoder.String(); err != nil {
// 		return
// 	}
// 	var itemBytes []byte
// 	itemBytes, err = decoder.Binary()
// 	if err != nil {
// 		return
// 	}
// 	item, err = wkstore.DecodeStreamItem(itemBytes)
// 	return
// }

func EncodeCMDChannelClusterConfigSave(channelID string, channelType uint8, data []byte) ([]byte, error) {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelID)
	encoder.WriteUint8(channelType)
	encoder.WriteBytes(data)
	return encoder.Bytes(), nil
}

func (c *CMD) DecodeCMDChannelClusterConfigSave() (channelID string, channelType uint8, data []byte, err error) {
	decoder := wkproto.NewDecoder(c.Data)

	if channelID, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	data, err = decoder.BinaryAll()
	return
}

func EncodeCMDBatchUpdateConversation(models []*wkdb.BatchUpdateConversationModel) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()

	encoder.WriteUint32(uint32(len(models)))
	for _, model := range models {
		encoder.WriteUint16(uint16(len(model.Uids)))
		for uid, seq := range model.Uids {
			encoder.WriteString(uid)
			encoder.WriteUint64(seq)
		}
		encoder.WriteString(model.ChannelId)
		encoder.WriteUint8(model.ChannelType)

	}

	return encoder.Bytes()
}

func (c *CMD) DecodeCMDBatchUpdateConversation() (models []*wkdb.BatchUpdateConversationModel, err error) {
	decoder := wkproto.NewDecoder(c.Data)

	var count uint32
	if count, err = decoder.Uint32(); err != nil {
		return
	}

	for i := uint32(0); i < count; i++ {
		var model = &wkdb.BatchUpdateConversationModel{
			Uids: map[string]uint64{},
		}
		var uidCount uint16
		if uidCount, err = decoder.Uint16(); err != nil {
			return
		}
		for j := uint16(0); j < uidCount; j++ {
			var uid string
			if uid, err = decoder.String(); err != nil {
				return
			}

			var seq uint64
			if seq, err = decoder.Uint64(); err != nil {
				return
			}
			model.Uids[uid] = seq
		}
		if model.ChannelId, err = decoder.String(); err != nil {
			return
		}
		if model.ChannelType, err = decoder.Uint8(); err != nil {
			return
		}
		models = append(models, model)
	}

	return
}

func EncodeCMDSystemUIDs(uids []string) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteUint32(uint32(len(uids)))
	for _, uid := range uids {
		encoder.WriteString(uid)
	}
	return encoder.Bytes()
}

func (c *CMD) DecodeCMDSystemUIDs() (uids []string, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	var count uint32
	if count, err = decoder.Uint32(); err != nil {
		return
	}
	for i := uint32(0); i < count; i++ {
		var uid string
		if uid, err = decoder.String(); err != nil {
			return
		}
		uids = append(uids, uid)
	}
	return
}

func EncodeCMDAddStreamMeta(streamMeta *wkdb.StreamMeta) []byte {
	return streamMeta.Encode()
}

func (c *CMD) DecodeCMDAddStreamMeta() (streamMeta *wkdb.StreamMeta, err error) {
	streamMeta = &wkdb.StreamMeta{}
	err = streamMeta.Decode(c.Data)
	return
}

func EncodeCMDAddStreams(streams []*wkdb.Stream) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteUint32(uint32(len(streams)))
	for _, stream := range streams {
		encoder.WriteBinary(stream.Encode())
	}
	return encoder.Bytes()
}

func (c *CMD) DecodeCMDAddStreams() ([]*wkdb.Stream, error) {
	decoder := wkproto.NewDecoder(c.Data)
	var count uint32
	var err error
	if count, err = decoder.Uint32(); err != nil {
		return nil, err
	}
	streams := make([]*wkdb.Stream, 0, count)
	for i := uint32(0); i < count; i++ {
		data, err := decoder.Binary()
		if err != nil {
			return nil, err
		}
		stream := &wkdb.Stream{}
		if err = stream.Decode(data); err != nil {
			return nil, err
		}
		streams = append(streams, stream)
	}
	return streams, nil
}

func EncodeCMDAddOrUpdateConversationsWithChannel(channelId string, channelType uint8, subscribers []string, readToMsgSeq uint64, conversationType wkdb.ConversationType, unreadCount int, createdAt, updatedAt int64) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelId)
	encoder.WriteUint8(channelType)
	encoder.WriteUint32(uint32(len(subscribers)))
	for _, subscriber := range subscribers {
		encoder.WriteString(subscriber)
	}
	encoder.WriteUint64(readToMsgSeq)
	encoder.WriteUint8(uint8(conversationType))
	encoder.WriteInt32(int32(unreadCount))
	encoder.WriteInt64(createdAt)
	encoder.WriteInt64(updatedAt)
	return encoder.Bytes()
}

func (c *CMD) DecodeCMDAddOrUpdateConversationsWithChannel() (channelId string, channelType uint8, subscribers []string, readToMsgSeq uint64, conversationType wkdb.ConversationType, unreadCount int, createdAt, updatedAt int64, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if channelId, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	var count uint32
	if count, err = decoder.Uint32(); err != nil {
		return
	}
	for i := uint32(0); i < count; i++ {
		var subscriber string
		if subscriber, err = decoder.String(); err != nil {
			return
		}
		subscribers = append(subscribers, subscriber)
	}
	if readToMsgSeq, err = decoder.Uint64(); err != nil {
		return
	}
	var conversationTypeUint8 uint8
	if conversationTypeUint8, err = decoder.Uint8(); err != nil {
		return
	}
	conversationType = wkdb.ConversationType(conversationTypeUint8)
	var unreadCountI32 int32
	if unreadCountI32, err = decoder.Int32(); err != nil {
		return
	}
	unreadCount = int(unreadCountI32)
	if createdAt, err = decoder.Int64(); err != nil {
		return
	}
	if updatedAt, err = decoder.Int64(); err != nil {
		return
	}
	return
}

func EncodeCMDAddOrUpdateConversations(conversations wkdb.ConversationSet) ([]byte, error) {

	return conversations.Marshal()
}

func (c *CMD) DecodeCMDAddOrUpdateConversations() (wkdb.ConversationSet, error) {
	conversations := wkdb.ConversationSet{}
	err := conversations.Unmarshal(c.Data)
	return conversations, err
}

func EncodeCMDAddOrUpdateTester(tester wkdb.Tester) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(tester.No)
	encoder.WriteString(tester.Addr)
	encoder.WriteUint64(uint64(tester.CreatedAt.UnixNano()))
	encoder.WriteUint64(uint64(tester.UpdatedAt.UnixNano()))
	return encoder.Bytes()
}

func (c *CMD) DecodeCMDAddOrUpdateTester() (tester wkdb.Tester, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if tester.No, err = decoder.String(); err != nil {
		return
	}
	if tester.Addr, err = decoder.String(); err != nil {
		return
	}
	var createdAtUnixNano uint64
	if createdAtUnixNano, err = decoder.Uint64(); err != nil {
		return
	}
	ct := time.Unix(int64(createdAtUnixNano/1e9), int64(createdAtUnixNano%1e9))
	tester.CreatedAt = &ct
	var updatedAtUnixNano uint64
	if updatedAtUnixNano, err = decoder.Uint64(); err != nil {
		return
	}
	ut := time.Unix(int64(updatedAtUnixNano/1e9), int64(updatedAtUnixNano%1e9))
	tester.UpdatedAt = &ut
	return
}

func EncodeCMDRemoveTester(no string) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(no)
	return encoder.Bytes()
}

func (c *CMD) DecodeCMDRemoveTester() (no string, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if no, err = decoder.String(); err != nil {
		return
	}
	return
}

var ErrStoreStopped = fmt.Errorf("store stopped")

type applyReq struct {
	logs  []types.Log
	waitC chan error
}
