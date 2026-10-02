package mqttsession

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ReplayMaintenance rotates bounded copy, recovery and consumer-retirement
// turns. Durable state stays in the cluster; Pass supplies scheduling only.
type ReplayMaintenance struct {
	replay     *ReplayCoordinator
	retirement *ReplayRetirement
}

func NewReplayMaintenance(replay *ReplayCoordinator, retirement *ReplayRetirement) (*ReplayMaintenance, error) {
	if replay == nil || retirement == nil {
		return nil, ErrInvalid
	}
	return &ReplayMaintenance{replay: replay, retirement: retirement}, nil
}

// Step executes exactly one phase. Continued journal scans retain their original
// pass; the managed worker yields after durable work/errors and advances passes.
func (m *ReplayMaintenance) Step(ctx context.Context, source meta.MQTTBindingOwner, cursor ReplayCursor) (out ReplayStepResult, err error) {
	pass := cursor.Pass
	out.Next.Pass = pass
	if m == nil || ctx == nil {
		return out, ErrInvalid
	}
	if pass%3 != 2 {
		if cursor.Retirement != (ReplayRetirementCursor{}) {
			return out, ErrEvidence
		}
		// Preserve the original two-phase replica/donor rotation after inserting
		// retirement. Arithmetic cannot overflow: floor(pass/3)*2 <= pass.
		cursor.Pass = (pass/3)*2 + pass%3
		out, err = m.replay.Step(ctx, source, cursor)
		out.Next.Pass = pass
		return out, err
	}
	if len(cursor.Targets) != 0 || cursor.NextTarget != 0 || cursor.RepairNext || cursor.Source != cursor.Retirement.Source || cursor.Authority != cursor.Retirement.Authority {
		return out, ErrEvidence
	}
	r, err := m.retirement.Step(ctx, source, cursor.Retirement)
	if err != nil {
		return out, err
	}
	out.RetirementCommitted, out.ContinueRetirement = r.Committed, r.ContinueScan
	if r.ContinueScan {
		out.Next = ReplayCursor{Pass: pass, Source: r.Next.Source, Authority: r.Next.Authority, Retirement: r.Next}
	}
	return out, nil
}
