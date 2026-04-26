package replica

import "github.com/WuKongIM/WuKongIM/pkg/channel"

func appendEpochPointInMemory(history []channel.EpochPoint, point channel.EpochPoint) ([]channel.EpochPoint, error) {
	if point.Epoch == 0 {
		return nil, channel.ErrCorruptState
	}
	if len(history) > 0 {
		last := history[len(history)-1]
		switch {
		case point.Epoch > last.Epoch:
			if point.StartOffset < last.StartOffset {
				return nil, channel.ErrCorruptState
			}
		case point.Epoch == last.Epoch && point.StartOffset == last.StartOffset:
			return history, nil
		default:
			return nil, channel.ErrCorruptState
		}
	}
	out := append([]channel.EpochPoint(nil), history...)
	out = append(out, point)
	return out, nil
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
