package clusterstore

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"go.uber.org/zap"
)

func (s *Store) AddOrUpdateUser(u wkdb.User) error {
	data := EncodeCMDUser(u)
	cmd := NewCMD(CMDAddOrUpdateUser, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		s.Error("marshal cmd failed", zap.Error(err))
		return err
	}
	slotId := s.opts.GetSlotId(u.Uid)
	_, err = s.opts.Cluster.ProposeDataToSlot(s.ctx, slotId, cmdData)
	return err
}

// AddOrUpdateDevice 添加或更新设备
func (s *Store) AddOrUpdateDevice(d wkdb.Device) error {
	data := EncodeCMDDevice(d)
	cmd := NewCMD(CMDAddOrUpdateDevice, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		s.Error("marshal cmd failed", zap.Error(err))
		return err
	}
	slotId := s.opts.GetSlotId(d.Uid)
	_, err = s.opts.Cluster.ProposeDataToSlot(s.ctx, slotId, cmdData)
	return err
}

// GetDevice 获取设备信息
func (s *Store) GetDevice(uid string, deviceFlag uint64) (wkdb.Device, error) {
	return s.wdb.GetDevice(uid, deviceFlag)
}
