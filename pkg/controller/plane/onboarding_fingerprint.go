package plane

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

// OnboardingPlanFingerprintInput contains the reviewed plan and strict view fields included in its digest.
type OnboardingPlanFingerprintInput struct {
	// TargetNode is the target node metadata observed when the plan was created.
	TargetNode controllermeta.ClusterNode
	// Plan is the deterministic onboarding plan approved by an operator.
	Plan controllermeta.NodeOnboardingPlan
	// Assignments provides config epochs and balance versions for each planned move slot.
	Assignments map[uint32]controllermeta.SlotAssignment
	// Runtime provides runtime safety observations for each planned move slot.
	Runtime map[uint32]controllermeta.SlotRuntimeView
	// Tasks records reconcile task presence for each planned move slot.
	Tasks map[uint32]controllermeta.ReconcileTask
	// MigratingSlots records hash-slot migration protections for each planned move slot.
	MigratingSlots map[uint32]struct{}
}

// OnboardingPlanFingerprint returns the SHA-256 hex digest of the canonical plan compatibility JSON.
func OnboardingPlanFingerprint(input OnboardingPlanFingerprintInput) string {
	sum := sha256.Sum256([]byte(OnboardingPlanCanonicalJSON(input)))
	return hex.EncodeToString(sum[:])
}

// OnboardingPlanCanonicalJSON returns the canonical JSON used by OnboardingPlanFingerprint.
func OnboardingPlanCanonicalJSON(input OnboardingPlanFingerprintInput) string {
	return canonicalJSON(onboardingFingerprintDocument(input))
}

func onboardingFingerprintDocument(input OnboardingPlanFingerprintInput) map[string]any {
	moves := make([]any, 0, len(input.Plan.Moves))
	for _, move := range input.Plan.Moves {
		assignment := input.Assignments[move.SlotID]
		view := input.Runtime[move.SlotID]
		_, taskPresent := input.Tasks[move.SlotID]
		_, migrating := input.MigratingSlots[move.SlotID]
		moves = append(moves, map[string]any{
			"assignment": map[string]any{
				"balance_version": assignment.BalanceVersion,
				"config_epoch":    assignment.ConfigEpoch,
			},
			"current_leader_id":        move.CurrentLeaderID,
			"desired_peers_after":      append([]uint64(nil), move.DesiredPeersAfter...),
			"desired_peers_before":     append([]uint64(nil), move.DesiredPeersBefore...),
			"hash_migration_absent":    !migrating,
			"leader_transfer_required": move.LeaderTransferRequired,
			"reason":                   move.Reason,
			"runtime": map[string]any{
				"current_peers":         append([]uint64(nil), view.CurrentPeers...),
				"has_quorum":            view.HasQuorum,
				"leader_id":             view.LeaderID,
				"observed_config_epoch": view.ObservedConfigEpoch,
			},
			"slot_id":        move.SlotID,
			"source_node_id": move.SourceNodeID,
			"target_node_id": move.TargetNodeID,
			"task_absent":    !taskPresent,
		})
	}

	return map[string]any{
		"moves": moves,
		"target": map[string]any{
			"join_state": input.TargetNode.JoinState,
			"node_id":    input.Plan.TargetNodeID,
			"role":       input.TargetNode.Role,
			"status":     input.TargetNode.Status,
		},
	}
}

func canonicalJSON(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return strconv.Quote(typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case int:
		return strconv.FormatInt(int64(typed), 10)
	case int8:
		return strconv.FormatInt(int64(typed), 10)
	case int16:
		return strconv.FormatInt(int64(typed), 10)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case uint:
		return strconv.FormatUint(uint64(typed), 10)
	case uint8:
		return strconv.FormatUint(uint64(typed), 10)
	case uint16:
		return strconv.FormatUint(uint64(typed), 10)
	case uint32:
		return strconv.FormatUint(uint64(typed), 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	case time.Time:
		return strconv.Quote(typed.UTC().Format(time.RFC3339Nano))
	case []any:
		return canonicalJSONArray(typed)
	case []uint64:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, item)
		}
		return canonicalJSONArray(items)
	case map[string]any:
		return canonicalJSONObject(typed)
	default:
		return canonicalReflectJSON(value)
	}
}

func canonicalJSONArray(values []any) string {
	out := "["
	for i, value := range values {
		if i > 0 {
			out += ","
		}
		out += canonicalJSON(value)
	}
	out += "]"
	return out
}

func canonicalJSONObject(values map[string]any) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := "{"
	for i, key := range keys {
		if i > 0 {
			out += ","
		}
		out += strconv.Quote(key) + ":" + canonicalJSON(values[key])
	}
	out += "}"
	return out
}

func canonicalReflectJSON(value any) string {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return "null"
	}
	for reflected.Kind() == reflect.Pointer || reflected.Kind() == reflect.Interface {
		if reflected.IsNil() {
			return "null"
		}
		reflected = reflected.Elem()
	}
	switch reflected.Kind() {
	case reflect.Slice, reflect.Array:
		items := make([]any, 0, reflected.Len())
		for i := 0; i < reflected.Len(); i++ {
			items = append(items, reflected.Index(i).Interface())
		}
		return canonicalJSONArray(items)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(reflected.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(reflected.Uint(), 10)
	case reflect.Bool:
		if reflected.Bool() {
			return "true"
		}
		return "false"
	case reflect.String:
		return strconv.Quote(reflected.String())
	default:
		panic(fmt.Sprintf("unsupported canonical JSON type %T", value))
	}
}
