package scripts_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudSimulationBootstrapSlotHealthUsesLegalActualLeader(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantLeaders  int
		wantReplicas int
	}{
		{
			name: "preferred mismatch remains healthy when actual leader is a voter",
			input: `{"items":[
				{"hash_slots":{"count":128},"state":{"quorum":"ready","sync":"matched","leader_match":true},"runtime":{"leader_id":1,"current_voters":[1,2,3]}},
				{"hash_slots":{"count":128},"state":{"quorum":"ready","sync":"matched","leader_match":false},"runtime":{"leader_id":2,"current_voters":[1,2,3]}}
			]}`,
			wantLeaders:  256,
			wantReplicas: 256,
		},
		{
			name: "missing or non-voter leader is unhealthy",
			input: `{"items":[
				{"hash_slots":{"count":64},"state":{"quorum":"ready","sync":"matched"},"runtime":{"leader_id":0,"current_voters":[1,2,3]}},
				{"hash_slots":{"count":64},"state":{"quorum":"ready","sync":"matched"},"runtime":{"leader_id":4,"current_voters":[1,2,3]}},
				{"hash_slots":{"count":64},"state":{"quorum":"quorum_lost","sync":"matched"},"runtime":{"leader_id":1,"current_voters":[1,2,3]}},
				{"hash_slots":{"count":64},"state":{"quorum":"ready","sync":"peer_mismatch"},"runtime":{"leader_id":1,"current_voters":[1,2,3]}}
			]}`,
			wantLeaders:  0,
			wantReplicas: 128,
		},
	}

	program := filepath.Join(repoRoot(t), "scripts", "cloud-sim", "bootstrap-slot-health.jq")
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command("jq", "-c", "-f", program)
			command.Stdin = strings.NewReader(test.input)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("evaluate slot health: %v\n%s", err, output)
			}
			var got struct {
				HealthySlotLeaders  int `json:"healthy_slot_leaders"`
				HealthySlotReplicas int `json:"healthy_slot_replicas"`
			}
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatalf("decode slot health: %v\n%s", err, output)
			}
			if got.HealthySlotLeaders != test.wantLeaders || got.HealthySlotReplicas != test.wantReplicas {
				t.Fatalf("slot health = leaders %d, replicas %d; want leaders %d, replicas %d", got.HealthySlotLeaders, got.HealthySlotReplicas, test.wantLeaders, test.wantReplicas)
			}
		})
	}
}

func TestCloudSimulationBootstrapCollectorDoesNotGatePreferredLeaderMatch(t *testing.T) {
	path := filepath.Join(repoRoot(t), "scripts", "cloud-sim", "collect-bootstrap-gate.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.Contains(text, `bootstrap-slot-health.jq`) {
		t.Fatal("Bootstrap Gate collector does not use the tested Slot health evaluator")
	}
	if strings.Contains(text, `leader_match == true`) {
		t.Fatal("Bootstrap Gate still treats PreferredLeader match as a hard health invariant")
	}
}
