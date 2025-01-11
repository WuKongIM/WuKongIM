package store

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"go.uber.org/zap"
)

func (s *Store) AddOrUpdateTester(u wkdb.Tester) error {
	data := EncodeCMDAddOrUpdateTester(u)
	cmd := NewCMD(CMDAddOrUpdateTester, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		s.Error("AddOrUpdateTester: marshal cmd failed", zap.Error(err))
		return err
	}
	var slotId uint32 = 0 // 默认数据在0槽位上
	_, err = s.opts.Slot.ProposeUntilApplied(slotId, cmdData)
	return err
}

func (s *Store) GetTester(no string) (wkdb.Tester, error) {
	return s.wdb.GetTester(no)
}

func (s *Store) GetTesters() ([]wkdb.Tester, error) {
	return s.wdb.GetTesters()
}

func (s *Store) RemoveTester(no string) error {
	data := EncodeCMDRemoveTester(no)
	cmd := NewCMD(CMDRemoveTester, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		s.Error("RemoveTester: marshal cmd failed", zap.Error(err))
		return err
	}
	var slotId uint32 = 0 // 默认数据在0槽位上
	_, err = s.opts.Slot.ProposeUntilApplied(slotId, cmdData)
	return err
}
