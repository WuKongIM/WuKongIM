package machine

import "sort"

// AdvanceHW recomputes the leader high watermark from ISR match offsets.
func (s *ChannelState) AdvanceHW() bool {
	if s.MinISR <= 0 || len(s.ISR) < s.MinISR {
		return false
	}
	matches := make([]uint64, 0, len(s.ISR))
	for _, replica := range s.ISR {
		matches = append(matches, s.Progress[replica].Match)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i] > matches[j] })
	next := matches[s.MinISR-1]
	if next <= s.HW {
		return false
	}
	s.HW = next
	return true
}
