package clusterstore

import (
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// UpdateUserToken 更新用户的token
func (s *Store) UpdateUserToken(uid string, deviceFlag uint8, deviceLevel uint8, token string) error {
	var (
		channelID   = uid
		channelType = wkproto.ChannelTypePerson
	)
	data := EncodeCMDUserToken(uid, deviceFlag, deviceLevel, token)
	cmd := NewCMD(CMDUpdateUserToken, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		s.Error("marshal cmd failed", zap.Error(err))
		return err
	}
	return s.opts.Cluster.ProposeMetaToChannel(channelID, channelType, cmdData)
}

// GetUserToken 获取用户的token和设备等级
func (s *Store) GetUserToken(uid string, deviceFlag uint8) (string, uint8, error) {
	return s.db.GetUserToken(uid, deviceFlag)
}
