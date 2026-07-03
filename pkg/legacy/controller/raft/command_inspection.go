package raft

import (
	"fmt"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/plane"
)

// CommandInspection is a redacted, JSON-friendly view of one Controller Raft command.
type CommandInspection struct {
	// Type is the stable machine-readable command type.
	Type string
	// Payload contains a JSON-friendly command summary with sensitive fields omitted.
	Payload map[string]any
}

// DecodeCommandInspection decodes one Controller Raft command into a redacted summary.
func DecodeCommandInspection(data []byte) (CommandInspection, error) {
	cmd, err := decodeCommand(data)
	if err != nil {
		return CommandInspection{}, err
	}
	return inspectCommand(cmd)
}

func inspectCommand(cmd slotcontroller.Command) (CommandInspection, error) {
	switch cmd.Kind {
	case slotcontroller.CommandKindNodeHeartbeat:
		payload := map[string]any{"command": "node_heartbeat"}
		if cmd.Report != nil {
			payload["node_id"] = cmd.Report.NodeID
			payload["capacity_weight"] = int64(cmd.Report.CapacityWeight)
			payload["hash_slot_table_version"] = cmd.Report.HashSlotTableVersion
			if cmd.Report.Runtime != nil {
				payload["runtime_slot_id"] = cmd.Report.Runtime.SlotID
			}
		}
		return simpleCommandInspection("node_heartbeat", payload), nil
	case slotcontroller.CommandKindOperatorRequest:
		payload := map[string]any{"command": "operator_request"}
		if cmd.Op != nil {
			payload["kind"] = operatorKindName(cmd.Op.Kind)
			payload["node_id"] = cmd.Op.NodeID
		}
		return simpleCommandInspection("operator_request", payload), nil
	case slotcontroller.CommandKindEvaluateTimeouts:
		return simpleCommandInspection("evaluate_timeouts", map[string]any{"command": "evaluate_timeouts"}), nil
	case slotcontroller.CommandKindTaskResult:
		payload := map[string]any{"command": "task_result"}
		if cmd.Advance != nil {
			payload["slot_id"] = cmd.Advance.SlotID
			payload["attempt"] = int64(cmd.Advance.Attempt)
			payload["has_error"] = cmd.Advance.Err != nil
		}
		return simpleCommandInspection("task_result", payload), nil
	case slotcontroller.CommandKindAssignmentTaskUpdate:
		payload := map[string]any{"command": "assignment_task_update"}
		if cmd.Assignment != nil {
			payload["slot_id"] = cmd.Assignment.SlotID
			payload["desired_peers"] = append([]uint64(nil), cmd.Assignment.DesiredPeers...)
			payload["preferred_leader"] = cmd.Assignment.PreferredLeader
			payload["config_epoch"] = cmd.Assignment.ConfigEpoch
		}
		if cmd.Task != nil {
			payload["task_kind"] = taskKindName(cmd.Task.Kind)
			payload["task_step"] = taskStepName(cmd.Task.Step)
			payload["task_status"] = taskStatusName(cmd.Task.Status)
		}
		return simpleCommandInspection("assignment_task_update", payload), nil
	case slotcontroller.CommandKindStartMigration:
		return migrationInspection("start_migration", cmd.Migration), nil
	case slotcontroller.CommandKindAdvanceMigration:
		return migrationInspection("advance_migration", cmd.Migration), nil
	case slotcontroller.CommandKindFinalizeMigration:
		return migrationInspection("finalize_migration", cmd.Migration), nil
	case slotcontroller.CommandKindAbortMigration:
		return migrationInspection("abort_migration", cmd.Migration), nil
	case slotcontroller.CommandKindAddSlot:
		payload := map[string]any{"command": "add_slot"}
		if cmd.AddSlot != nil {
			payload["new_slot_id"] = cmd.AddSlot.NewSlotID
			payload["peers"] = append([]uint64(nil), cmd.AddSlot.Peers...)
			payload["preferred_leader"] = cmd.AddSlot.PreferredLeader
		}
		return simpleCommandInspection("add_slot", payload), nil
	case slotcontroller.CommandKindRemoveSlot:
		payload := map[string]any{"command": "remove_slot"}
		if cmd.RemoveSlot != nil {
			payload["slot_id"] = cmd.RemoveSlot.SlotID
		}
		return simpleCommandInspection("remove_slot", payload), nil
	case slotcontroller.CommandKindNodeStatusUpdate:
		payload := map[string]any{"command": "node_status_update"}
		if cmd.NodeStatusUpdate != nil {
			transitions := make([]any, 0, len(cmd.NodeStatusUpdate.Transitions))
			for _, transition := range cmd.NodeStatusUpdate.Transitions {
				item := map[string]any{
					"node_id":    transition.NodeID,
					"new_status": nodeStatusName(transition.NewStatus),
				}
				if transition.ExpectedStatus != nil {
					item["expected_status"] = nodeStatusName(*transition.ExpectedStatus)
				}
				transitions = append(transitions, item)
			}
			payload["transitions"] = transitions
		}
		return simpleCommandInspection("node_status_update", payload), nil
	case slotcontroller.CommandKindNodeJoin:
		payload := map[string]any{"command": "node_join"}
		if cmd.NodeJoin != nil {
			payload["node_id"] = cmd.NodeJoin.NodeID
			payload["name"] = cmd.NodeJoin.Name
			payload["capacity_weight"] = int64(cmd.NodeJoin.CapacityWeight)
		}
		return simpleCommandInspection("node_join", payload), nil
	case slotcontroller.CommandKindNodeJoinActivate:
		payload := map[string]any{"command": "node_join_activate"}
		if cmd.NodeJoinActivate != nil {
			payload["node_id"] = cmd.NodeJoinActivate.NodeID
		}
		return simpleCommandInspection("node_join_activate", payload), nil
	case slotcontroller.CommandKindNodeOnboardingJobUpdate:
		payload := map[string]any{"command": "node_onboarding_job_update"}
		if cmd.NodeOnboarding != nil {
			if cmd.NodeOnboarding.Job != nil {
				payload["job_id"] = cmd.NodeOnboarding.Job.JobID
				payload["target_node_id"] = cmd.NodeOnboarding.Job.TargetNodeID
				payload["status"] = string(cmd.NodeOnboarding.Job.Status)
			}
			if cmd.NodeOnboarding.ExpectedStatus != nil {
				payload["expected_status"] = string(*cmd.NodeOnboarding.ExpectedStatus)
			}
			if cmd.NodeOnboarding.Assignment != nil {
				payload["slot_id"] = cmd.NodeOnboarding.Assignment.SlotID
			}
		}
		return simpleCommandInspection("node_onboarding_job_update", payload), nil
	default:
		return CommandInspection{}, fmt.Errorf("controllerraft: unsupported command inspection kind %d", cmd.Kind)
	}
}

func simpleCommandInspection(commandType string, payload map[string]any) CommandInspection {
	if payload == nil {
		payload = map[string]any{"command": commandType}
	}
	return CommandInspection{Type: commandType, Payload: payload}
}

func migrationInspection(commandType string, migration *slotcontroller.MigrationRequest) CommandInspection {
	payload := map[string]any{"command": commandType}
	if migration != nil {
		payload["hash_slot"] = int64(migration.HashSlot)
		payload["source"] = migration.Source
		payload["target"] = migration.Target
		payload["phase"] = int64(migration.Phase)
	}
	return simpleCommandInspection(commandType, payload)
}

func operatorKindName(kind slotcontroller.OperatorKind) string {
	switch kind {
	case slotcontroller.OperatorMarkNodeDraining:
		return "mark_node_draining"
	case slotcontroller.OperatorResumeNode:
		return "resume_node"
	default:
		return "unknown"
	}
}

func nodeStatusName(status controllermeta.NodeStatus) string {
	switch status {
	case controllermeta.NodeStatusAlive:
		return "alive"
	case controllermeta.NodeStatusSuspect:
		return "suspect"
	case controllermeta.NodeStatusDead:
		return "dead"
	case controllermeta.NodeStatusDraining:
		return "draining"
	default:
		return "unknown"
	}
}

func taskKindName(kind controllermeta.TaskKind) string {
	switch kind {
	case controllermeta.TaskKindBootstrap:
		return "bootstrap"
	case controllermeta.TaskKindRepair:
		return "repair"
	case controllermeta.TaskKindRebalance:
		return "rebalance"
	case controllermeta.TaskKindLeaderTransfer:
		return "leader_transfer"
	default:
		return "unknown"
	}
}

func taskStepName(step controllermeta.TaskStep) string {
	switch step {
	case controllermeta.TaskStepAddLearner:
		return "add_learner"
	case controllermeta.TaskStepCatchUp:
		return "catch_up"
	case controllermeta.TaskStepPromote:
		return "promote"
	case controllermeta.TaskStepTransferLeader:
		return "transfer_leader"
	case controllermeta.TaskStepRemoveOld:
		return "remove_old"
	default:
		return "unknown"
	}
}

func taskStatusName(status controllermeta.TaskStatus) string {
	switch status {
	case controllermeta.TaskStatusPending:
		return "pending"
	case controllermeta.TaskStatusRetrying:
		return "retrying"
	case controllermeta.TaskStatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}
