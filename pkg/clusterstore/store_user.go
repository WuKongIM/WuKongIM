package clusterstore

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// UpdateUser 更新用户
func (s *Store) UpdateUser(u wkdb.User) error {
	var (
		channelID   = u.Uid
		channelType = wkproto.ChannelTypePerson
	)
	data := EncodeCMDUser(u)
	cmd := NewCMD(CMDUpdateUser, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		s.Error("marshal cmd failed", zap.Error(err))
		return err
	}
	_, err = s.opts.Cluster.ProposeChannelMeta(s.ctx, channelID, channelType, cmdData)
	return err
}

// GetUserToken 获取用户的token和设备等级
func (s *Store) GetUser(uid string, deviceFlag uint8) (wkdb.User, error) {
	return s.wdb.GetUser(uid, deviceFlag)
}
