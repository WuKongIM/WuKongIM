package store

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (s *Store) AddUser(u wkdb.User) error {

	data := EncodeCMDUser(u)
	cmd := NewCMD(CMDAddUser, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		s.Error("marshal cmd failed", zap.Error(err))
		return err
	}
	slotId := s.opts.Slot.GetSlotId(u.Uid)

	_, err = s.opts.Slot.ProposeUntilApplied(slotId, cmdData)
	return err
}

func (s *Store) GetUser(uid string) (wkdb.User, error) {
	return s.wdb.GetUser(uid)
}

func (s *Store) UpdateUser(u wkdb.User) error {
	data := EncodeCMDUser(u)
	cmd := NewCMD(CMDUpdateUser, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		s.Error("marshal cmd failed", zap.Error(err))
		return err
	}
	slotId := s.opts.Slot.GetSlotId(u.Uid)
	_, err = s.opts.Slot.ProposeUntilApplied(slotId, cmdData)
	return err
}

func (s *Store) UpdateDevice(d wkdb.Device) error {
	data := EncodeCMDDevice(d)
	cmd := NewCMD(CMDUpdateDevice, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		s.Error("marshal cmd failed", zap.Error(err))
		return err
	}

	slotId := s.opts.Slot.GetSlotId(d.Uid)
	_, err = s.opts.Slot.ProposeUntilApplied(slotId, cmdData)
	return err
}

func (s *Store) AddDevice(d wkdb.Device) error {
	data := EncodeCMDDevice(d)
	cmd := NewCMD(CMDAddDevice, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		s.Error("marshal cmd failed", zap.Error(err))
		return err
	}

	slotId := s.opts.Slot.GetSlotId(d.Uid)
	_, err = s.opts.Slot.ProposeUntilApplied(slotId, cmdData)
	return err
}

func (s *Store) GetDevice(uid string, deviceFlag wkproto.DeviceFlag) (wkdb.Device, error) {
	return s.wdb.GetDevice(uid, uint64(deviceFlag))
}

func (s *Store) GetSystemUids() ([]string, error) {
	return s.wdb.GetSystemUids()
}

func (s *Store) AddSystemUids(uids []string) error {

	data := EncodeCMDSystemUIDs(uids)
	cmd := NewCMD(CMDSystemUIDsAdd, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	var slotId uint32 = 0 // 系统uid默认存储在slot 0上
	_, err = s.opts.Slot.ProposeUntilApplied(slotId, cmdData)
	return err
}

func (s *Store) RemoveSystemUids(uids []string) error {
	data := EncodeCMDSystemUIDs(uids)
	cmd := NewCMD(CMDSystemUIDsRemove, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	var slotId uint32 = 0 // 系统uid默认存储在slot 0上
	_, err = s.opts.Slot.ProposeUntilApplied(slotId, cmdData)
	return err
}
