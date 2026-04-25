package replica

import "github.com/WuKongIM/WuKongIM/pkg/channel"

func (r *replica) appendEpochPointLocked(point channel.EpochPoint) error {
	if len(r.epochHistory) > 0 {
		last := r.epochHistory[len(r.epochHistory)-1]
		switch {
		case point.Epoch > last.Epoch:
			if point.StartOffset < last.StartOffset {
				return channel.ErrCorruptState
			}
		case point.Epoch == last.Epoch && point.StartOffset == last.StartOffset:
			return nil
		default:
			return channel.ErrCorruptState
		}
	}

	if err := r.history.Append(point); err != nil {
		return err
	}
	r.epochHistory = append(r.epochHistory, point)
	return nil
}

func (r *replica) truncateLogToLocked(to uint64) error {
	if err := r.log.Truncate(to); err != nil {
		return err
	}
	if err := r.log.Sync(); err != nil {
		return err
	}
	if err := r.history.TruncateTo(to); err != nil {
		return err
	}
	leo := r.log.LEO()
	if leo != to {
		return channel.ErrCorruptState
	}
	r.epochHistory = trimEpochHistoryToLEO(r.epochHistory, to)
	return nil
}

func trimEpochHistoryToLEO(history []channel.EpochPoint, leo uint64) []channel.EpochPoint {
	if len(history) == 0 {
		return nil
	}

	end := 0
	for end < len(history) && history[end].StartOffset <= leo {
		end++
	}
	if end == 0 {
		return nil
	}
	return append([]channel.EpochPoint(nil), history[:end]...)
}

func offsetEpochForLEO(history []channel.EpochPoint, leo uint64) uint64 {
	if len(history) == 0 {
		return 0
	}

	var epoch uint64
	for _, point := range history {
		if point.StartOffset > leo {
			break
		}
		epoch = point.Epoch
	}
	return epoch
}
