package nodeops

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	contextcmd "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/context"
)

func TestNodeListCommandPrintsLifecycleAndHealthEvidence(t *testing.T) {
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/manager/nodes" {
			t.Fatalf("path = %s, want /manager/nodes", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"items":[{"node_id":4,"name":"node-4","membership":{"join_state":"joining","schedulable":false},"health":{"fresh":true,"freshness":"fresh","status":"alive","runtime_ready":true,"observed_control_revision":7}}],"total":1}`))
	}))
	defer manager.Close()

	stdout, stderr, err := executeNodeCommand("ls", "--server", manager.URL)
	if err != nil {
		t.Fatalf("node ls error = %v stdout %q stderr %q", err, stdout, stderr)
	}
	for _, want := range []string{
		"node-4",
		"node=4",
		"join_state=joining",
		"schedulable=false",
		"health=fresh/alive",
		"fresh=true",
		"runtime_ready=true",
		"control_rev=7",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("node ls stdout missing %q: %q", want, stdout)
		}
	}
}

func TestNodeScaleInStatusCommandPrintsRootCauseBlockers(t *testing.T) {
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/manager/nodes/4/scale-in/status" {
			t.Fatalf("path = %s, want /manager/nodes/4/scale-in/status", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"node_id":4,
			"join_state":"leaving",
			"state_revision":88,
			"safe_to_proceed":false,
			"safe_to_remove":false,
			"blocked_by_missing_node":true,
			"blocked_by_join_state":true,
			"blocked_by_controller_role":true,
			"blocked_by_data_role":true,
			"blocked_by_health":true,
			"blocked_by_runtime_drain":true,
			"blocked_by_control_revision":true,
			"blocked_by_channels":true,
			"unknown_runtime":true,
			"unknown_control_revision":true,
			"unknown_channel_inventory":true,
			"health_fresh":false,
			"health_freshness":"stale",
			"health_status":"alive",
			"health_report_age_ms":1234,
			"health_report_ttl_ms":5000,
			"observed_control_revision":80,
			"required_control_revision":88,
			"blocked_reasons":["target_health_stale","gateway_sessions_present"],
			"slot_replica_count":12,
			"slot_leader_count":1,
			"active_task_count":3,
			"failed_task_count":1,
			"channel_leader_count":5,
			"channel_replica_count":9,
			"channel_isr_count":8,
			"gateway_draining":true,
			"accepting_new_sessions":false,
			"gateway_sessions":2,
			"active_online":2,
			"closing_online":1,
			"pending_activations":4
		}`))
	}))
	defer manager.Close()

	stdout, stderr, err := executeNodeCommand("scale-in", "status", "4", "--server", manager.URL)
	if err != nil {
		t.Fatalf("node scale-in status error = %v stdout %q stderr %q", err, stdout, stderr)
	}
	for _, want := range []string{
		"node=4",
		"join_state=leaving",
		"state_revision=88",
		"safe_to_proceed=false",
		"safe_to_remove=false",
		"target_health_stale",
		"gateway_sessions_present",
		"health=stale/alive",
		"fresh=false",
		"control_rev=80/88",
		"slots replicas=12 leaders=1",
		"tasks active=3 failed=1",
		"channels leader=5 replica=9 isr=8",
		"gateway draining=true accepting_new_sessions=false gateway_sessions=2 active_online=2 closing_online=1 pending_activations=4",
		"missing_node=true",
		"join_state_blocked=true",
		"controller_role=true",
		"data_role=true",
		"blocked_by_health=true",
		"blocked_by_runtime_drain=true",
		"blocked_by_control_revision=true",
		"blocked_by_channels=true",
		"unknown_runtime=true",
		"unknown_control_revision=true",
		"unknown_channel_inventory=true",
		"health_report_age_ms=1234",
		"health_report_ttl_ms=5000",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("scale-in status stdout missing %q: %q", want, stdout)
		}
	}
}

func TestNodeCommandUsesFirstContextServerAndPrintsEvidence(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/manager/nodes" {
			t.Fatalf("path = %s, want /manager/nodes", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"items":[],"total":0}`))
	}))
	defer first.Close()

	secondCalled := false
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondCalled = true
		http.Error(w, "second server must not be called", http.StatusInternalServerError)
	}))
	defer second.Close()

	contextDir := t.TempDir()
	store := contextcmd.NewStore(contextDir)
	if err := store.Save(contextcmd.Context{Name: "ops", Servers: []string{first.URL, second.URL}}); err != nil {
		t.Fatalf("save context: %v", err)
	}
	if err := store.Select("ops"); err != nil {
		t.Fatalf("select context: %v", err)
	}

	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr, ContextDir: &contextDir})
	cmd.SetArgs([]string{"ls"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("node ls error = %v stdout %q stderr %q", err, stdout.String(), stderr.String())
	}
	if secondCalled {
		t.Fatal("second context server was called; want only first server")
	}
	wantEvidence := fmt.Sprintf("using first manager server from context ops: %s", first.URL)
	if !strings.Contains(stderr.String(), wantEvidence) {
		t.Fatalf("stderr missing %q: %q", wantEvidence, stderr.String())
	}
}

func TestNodeCommandJSONPreservesDecodedManagerFields(t *testing.T) {
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manager/nodes":
			_, _ = w.Write([]byte(`{"items":[],"total":0,"manager_extra":"kept"}`))
		case "/manager/nodes/4/scale-in/status":
			_, _ = w.Write([]byte(`{"node_id":4,"safe_to_remove":false,"operator_note":"kept"}`))
		default:
			t.Fatalf("path = %s, want node read endpoint", r.URL.Path)
		}
	}))
	defer manager.Close()

	stdout, stderr, err := executeNodeCommand("ls", "--server", manager.URL, "--json")
	if err != nil {
		t.Fatalf("node ls --json error = %v stdout %q stderr %q", err, stdout, stderr)
	}
	if !strings.Contains(stdout, `"manager_extra": "kept"`) {
		t.Fatalf("node ls --json did not preserve manager_extra: %q", stdout)
	}

	stdout, stderr, err = executeNodeCommand("scale-in", "status", "4", "--server", manager.URL, "--json")
	if err != nil {
		t.Fatalf("node scale-in status --json error = %v stdout %q stderr %q", err, stdout, stderr)
	}
	if !strings.Contains(stdout, `"operator_note": "kept"`) {
		t.Fatalf("node scale-in status --json did not preserve operator_note: %q", stdout)
	}
}

func executeNodeCommand(args ...string) (string, string, error) {
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}
