package store

import "github.com/WuKongIM/WuKongIM/pkg/wkdb"

func (s *Store) SaveChannelClusterConfig(cfg wkdb.ChannelClusterConfig) error {
	cfgData, err := cfg.Marshal()
	if err != nil {
		return err
	}

	data, err := EncodeCMDChannelClusterConfigSave(cfg.ChannelId, cfg.ChannelType, cfgData)
	if err != nil {
		return err
	}
	cmd := NewCMD(CMDChannelClusterConfigSave, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	slotId := s.opts.Slot.GetSlotId(cfg.ChannelId)
	_, err = s.opts.Slot.ProposeUntilApplied(slotId, cmdData)
	return err
}
