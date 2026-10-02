package message

import "context"

// PrepareMQTTReplay returns an existing covered page before extending shared
// content. Lost bounded-page replies can therefore retry their original wider
// request without attempting an overlapping write. Concurrent new extensions
// may conflict and retry; this method never resets or releases source progress.
func (s *ChannelStore) PrepareMQTTReplay(ctx context.Context, generation string, from, through uint64, opts ReadOptions) (page MQTTReplayTransfer, err error) {
	if err = validateMQTTReplayRead(generation, from, through, opts); err != nil {
		return page, toChannelError(err)
	}
	if err = s.beginUse(); err != nil {
		return page, err
	}
	defer s.endUse()
	defer func() { err = toChannelError(err) }()
	current, present, err := s.log.LoadMQTTReplayState(ctx)
	if err != nil {
		return page, err
	}
	if present && current.Generation == generation && from <= current.Through {
		return s.log.ExportMQTTReplay(ctx, generation, from, min(through, current.Through), opts)
	}
	copied, err := s.log.CopyMQTTReplaySource(ctx, generation, from, through, opts)
	if err != nil {
		return page, err
	}
	return s.log.ExportMQTTReplay(ctx, generation, from, copied.Through, opts)
}
