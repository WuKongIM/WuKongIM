package presencelog

import (
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// AppendRecoveryProviderFailure publishes fixed scalar metadata only on the
// existing failed activation event. Missing stages remain absent, not zero.
func AppendRecoveryProviderFailure(fields []wklog.Field, err error) []wklog.Field {
	e, ok := presence.RecoveryProviderFailure(err)
	if !ok {
		return fields
	}
	fields = append(fields,
		wklog.String("recovery_provider_scope", "physical_round"),
		wklog.String("recovery_provider_reason", e.Reason.String()),
		wklog.Uint64("recovery_provider_observed", uint64(e.Observed)),
		wklog.Int64("recovery_provider_failure_ns", int64(e.FailureElapsed)),
		wklog.Bool("recovery_provider_context_override", e.ContextOverride),
		wklog.Uint64("recovery_provider_calls", e.Calls),
		wklog.Uint64("recovery_provider_owners_planned", e.OwnersPlanned),
		wklog.Uint64("recovery_provider_owner_scans_completed", e.OwnersCompleted))
	keys := [...]string{"recovery_provider_reserve_ns", "recovery_provider_admission_ns", "recovery_provider_target_before_ns", "recovery_provider_snapshot_before_ns", "recovery_provider_owner_read_ns", "recovery_provider_snapshot_after_ns", "recovery_provider_target_after_ns"}
	for stage, key := range keys {
		if e.Observed&presence.RecoveryProviderStage(stage).Mask() != 0 {
			fields = append(fields, wklog.Int64(key, int64(e.Stages[stage])))
		}
	}
	if e.Observed&presence.RecoveryProviderObservedOwnerBudget != 0 {
		fields = append(fields, wklog.Uint64("recovery_provider_owner_node", e.OwnerNodeID), wklog.Int64("recovery_provider_owner_configured_ns", int64(e.OwnerConfiguredBudget)), wklog.Uint64("recovery_provider_page_start", e.PageStart))
	}
	if e.Observed&presence.RecoveryProviderObservedOwnerDispatch != 0 {
		fields = append(fields, wklog.String("recovery_provider_owner_mode", e.OwnerMode.String()), wklog.Int64("recovery_provider_owner_effective_ns", int64(e.OwnerEffectiveBudget)))
	}
	if e.Observed&presence.RecoveryProviderObservedOwnerElapsed != 0 {
		fields = append(fields, wklog.Int64("recovery_provider_owner_elapsed_ns", int64(e.OwnerElapsed)))
	}
	if e.Observed&presence.RecoveryProviderObservedOwnerError != 0 {
		fields = append(fields, wklog.String("recovery_provider_owner_error", e.OwnerError.String()))
	}
	if e.Observed&presence.RecoveryProviderObservedPageNext != 0 {
		fields = append(fields, wklog.Uint64("recovery_provider_page_next", e.PageNext))
	}
	if e.OwnerRPCTrace.State != 0 {
		fields = append(fields, wklog.String("recovery_provider_owner_trace_state", e.OwnerRPCTrace.State.String()))
		if e.OwnerRPCTrace.TraceID != "" {
			fields = append(fields, wklog.String("recovery_provider_owner_trace_id", e.OwnerRPCTrace.TraceID))
		}
	}
	return fields
}
