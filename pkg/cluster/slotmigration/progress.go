package slotmigration

import "time"

type Progress struct {
	SourceApplyIndex  uint64
	TargetApplyIndex  uint64
	DeltaLag          int64
	StableWindowStart time.Time
	BytesTransferred  int64
}

func (p *Progress) IsStable(threshold int64, window time.Duration) bool {
	return p.isStableAt(threshold, window, time.Now())
}

func (p *Progress) isStableAt(threshold int64, window time.Duration, now time.Time) bool {
	if p == nil || p.DeltaLag >= threshold || p.StableWindowStart.IsZero() {
		return false
	}
	return now.Sub(p.StableWindowStart) > window
}

func (p *Progress) Update(sourceApplyIndex, targetApplyIndex uint64, bytesTransferred int64, stableThreshold int64, now time.Time) {
	if p == nil {
		return
	}
	p.SourceApplyIndex = sourceApplyIndex
	p.TargetApplyIndex = targetApplyIndex
	if sourceApplyIndex > targetApplyIndex {
		p.DeltaLag = int64(sourceApplyIndex - targetApplyIndex)
	} else {
		p.DeltaLag = 0
	}
	if bytesTransferred > p.BytesTransferred {
		p.BytesTransferred = bytesTransferred
	}
	if p.DeltaLag < stableThreshold {
		if p.StableWindowStart.IsZero() {
			p.StableWindowStart = now
		}
		return
	}
	p.StableWindowStart = time.Time{}
}
