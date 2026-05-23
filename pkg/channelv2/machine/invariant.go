package machine

import ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"

// CheckInvariants validates cheap runtime ordering invariants.
func (s *ChannelState) CheckInvariants() error {
	if s.CheckpointHW > s.HW || s.HW > s.LEO {
		return ch.ErrInvalidConfig
	}
	return nil
}
