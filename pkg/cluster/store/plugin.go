package store

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
)

// UpdateUserPluginNo 更新用户插件编号
func (s *Store) UpdateUserPluginNo(uid string, pluginNo string) error {
	data := EncodeCMDUserPluginNo(wkdb.PluginUser{
		Uid:       uid,
		PluginNo:  pluginNo,
		CreatedAt: wkutil.TimePtr(time.Now()),
		UpdatedAt: wkutil.TimePtr(time.Now()),
	})
	cmd := NewCMD(CMDUpdateUserPluginNo, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	var slotId uint32 = 0 // 默认数据在0槽位上
	_, err = s.opts.Slot.ProposeUntilApplied(slotId, cmdData)
	return err
}

func (s *Store) RemovePluginUser(uid string, pluginNo string) error {
	data := EncodeCMDPluginUser(pluginNo, uid)
	cmd := NewCMD(CMDRemovePluginUser, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	var slotId uint32 = 0 // 默认数据在0槽位上
	_, err = s.opts.Slot.ProposeUntilApplied(slotId, cmdData)
	return err
}

// // AddOrUpdatePlugin 添加或更新插件
// func (s *Store) AddOrUpdatePlugin(plugin wkdb.Plugin) error {
// 	data := EncodeCMDPlugin(plugin)
// 	cmd := NewCMD(CMDAddOrUpdatePlugin, data)
// 	cmdData, err := cmd.Marshal()
// 	if err != nil {
// 		return err
// 	}
// 	var slotId uint32 = 0 // 默认数据在0槽位上
// 	_, err = s.opts.Slot.ProposeUntilApplied(slotId, cmdData)
// 	return err
// }

// // UpdatePluginConfig 更新插件配置
// func (s *Store) UpdatePluginConfig(pluginNo string, config map[string]interface{}) error {
// 	data := EncodeCMDPluginConfig(pluginNo, config)
// 	cmd := NewCMD(CMDUpdatePluginConfig, data)
// 	cmdData, err := cmd.Marshal()
// 	if err != nil {
// 		return err
// 	}
// 	var slotId uint32 = 0 // 默认数据在0槽位上
// 	_, err = s.opts.Slot.ProposeUntilApplied(slotId, cmdData)
// 	return err
// }
