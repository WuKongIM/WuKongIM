package store

import "github.com/WuKongIM/WuKongIM/pkg/wkdb"

func (s *Store) SaveChannelClusterConfig(cfg wkdb.ChannelClusterConfig) (version uint64, err error) {
	cfgData, err := cfg.Marshal()
	if err != nil {
		return 0, err
	}

	data, err := EncodeCMDChannelClusterConfigSave(cfg.ChannelId, cfg.ChannelType, cfgData)
	if err != nil {
		return 0, err
	}
	cmd := NewCMD(CMDChannelClusterConfigSave, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return 0, err
	}
	slotId := s.opts.Slot.GetSlotId(cfg.ChannelId)
	resp, err := s.opts.Slot.ProposeUntilApplied(slotId, cmdData)
	if err != nil {
		return 0, err
	}
	return resp.Index, nil
}
