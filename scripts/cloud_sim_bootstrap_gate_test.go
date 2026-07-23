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

func TestCloudSimulationBootstrapCollectorCapturesEffectiveRuntimeContract(t *testing.T) {
	path := filepath.Join(repoRoot(t), "scripts", "cloud-sim", "collect-bootstrap-gate.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	programPath := filepath.Join(repoRoot(t), "scripts", "cloud-sim", "bootstrap-runtime-contract.jq")
	program, err := os.ReadFile(programPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content) + "\n" + string(program)
	for _, required := range []string{
		`/manager/nodes/${node_id}/config`,
		`--argjson node_id "$node_id"`,
		`bootstrap-runtime-contract.jq`,
		`sudo cat /etc/wukongim/scenario.yaml`,
		`sudo cat /etc/wukongim/effective-node-runtime-contract.json`,
		`read-scenario-scale.awk`,
		`effective-node-runtime-contract/v1`,
		`WK_CLUSTER_HASH_SLOT_COUNT`,
		`WK_CLUSTER_INITIAL_SLOT_COUNT`,
		`WK_CLUSTER_CHANNEL_REACTOR_COUNT`,
		`WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS`,
		`WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS`,
		`WK_CLUSTER_CHANNEL_RPC_WORKERS`,
		`WK_GATEWAY_GNET_NUM_EVENT_LOOP`,
		`WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS`,
		`WK_GATEWAY_RUNTIME_ASYNC_SEND_QUEUE_CAPACITY`,
		`WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY`,
		`WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS`,
		`node_runtime_contracts`,
		`expected_node_runtime_contract`,
		`runtime_scale`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("Bootstrap Gate collector missing effective runtime contract %q", required)
		}
	}
}

func TestCloudSimulationBootstrapScenarioScaleParserAcceptsRenderedIndentation(t *testing.T) {
	program := filepath.Join(repoRoot(t), "scripts", "cloud-sim", "read-scenario-scale.awk")
	for _, testCase := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "source two spaces", input: "version: wkbench/v1\nobjectives:\n  scale: medium\nlimits: {}\n", want: "medium\n"},
		{name: "yaml v3 four spaces", input: "version: wkbench/v1\nobjectives:\n    scale: medium\n    standard: true\nlimits: {}\n", want: "medium\n"},
		{name: "quoted with comment", input: "objectives:\n    scale: 'large' # reviewed\n", want: "large\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command := exec.Command("awk", "-f", program)
			command.Stdin = strings.NewReader(testCase.input)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("parse scale: %v\n%s", err, output)
			}
			if string(output) != testCase.want {
				t.Fatalf("scale output = %q, want %q", output, testCase.want)
			}
		})
	}

	for _, input := range []string{
		"objectives:\n    standard: true\n",
		"objectives:\n    scale: unsupported\n",
		"objectives:\nlimits: {}\n",
	} {
		command := exec.Command("awk", "-f", program)
		command.Stdin = strings.NewReader(input)
		if output, err := command.CombinedOutput(); err == nil {
			t.Fatalf("invalid scale unexpectedly passed: output=%q input=%q", output, input)
		}
	}
}

func TestCloudSimulationBootstrapRuntimeContractUsesNormalizedManagerValues(t *testing.T) {
	input := `{"node_id":2,"source":"effective_startup_config","requires_restart":true,"groups":[{"items":[
		{"key":"WK_CLUSTER_HASH_SLOT_COUNT","value":"256","source":"toml"},
		{"key":"WK_CLUSTER_INITIAL_SLOT_COUNT","value":"10","source":"toml"},
		{"key":"WK_CLUSTER_SLOT_REPLICA_N","value":"3","source":"toml"},
		{"key":"WK_CLUSTER_CHANNEL_REPLICA_N","value":"3","source":"toml"},
		{"key":"WK_CLUSTER_CHANNEL_REACTOR_COUNT","value":"4","source":"toml"},
		{"key":"WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS","value":"8","source":"toml"},
		{"key":"WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS","value":"8","source":"toml"},
		{"key":"WK_CLUSTER_CHANNEL_RPC_WORKERS","value":"50","source":"toml"},
		{"key":"WK_GATEWAY_GNET_MULTICORE","value":"true","source":"toml"},
		{"key":"WK_GATEWAY_GNET_NUM_EVENT_LOOP","value":"4","source":"toml"},
		{"key":"WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS","value":"128","source":"toml"},
		{"key":"WK_GATEWAY_RUNTIME_ASYNC_SEND_QUEUE_CAPACITY","value":"131072","source":"toml"},
		{"key":"WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY","value":"320","source":"toml"},
		{"key":"WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS","value":"750000","source":"toml"}
	]}]}`
	program := filepath.Join(repoRoot(t), "scripts", "cloud-sim", "bootstrap-runtime-contract.jq")
	command := exec.Command("jq", "-c", "-e", "--arg", "scale", "medium", "--argjson", "node_id", "2", "-f", program)
	command.Stdin = strings.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build runtime contract: %v\n%s", err, output)
	}
	var contract struct {
		Schema                            string            `json:"schema"`
		Scale                             string            `json:"scale"`
		PhysicalHashSlotCount             int               `json:"physical_hash_slot_count"`
		LogicalSlotGroupCount             int               `json:"logical_slot_group_count"`
		ChannelRPCWorkers                 int               `json:"channel_rpc_workers"`
		RecipientWorkerConcurrency        int               `json:"recipient_worker_concurrency"`
		ConversationAuthorityCacheMaxRows int               `json:"conversation_authority_cache_max_rows"`
		ValueSources                      map[string]string `json:"value_sources"`
	}
	if err := json.Unmarshal(output, &contract); err != nil {
		t.Fatalf("decode runtime contract: %v\n%s", err, output)
	}
	if contract.Schema != "wukongim/cloud-effective-node-runtime-contract/v1" || contract.Scale != "medium" ||
		contract.PhysicalHashSlotCount != 256 || contract.LogicalSlotGroupCount != 10 ||
		contract.ChannelRPCWorkers != 50 || contract.RecipientWorkerConcurrency != 320 ||
		contract.ConversationAuthorityCacheMaxRows != 750000 || len(contract.ValueSources) != 14 {
		t.Fatalf("runtime contract = %#v", contract)
	}
	for key, source := range contract.ValueSources {
		if source != "toml" {
			t.Fatalf("runtime contract source %s = %q, want toml", key, source)
		}
	}

	missing := strings.Replace(input, `{"key":"WK_CLUSTER_CHANNEL_RPC_WORKERS","value":"50","source":"toml"},`, "", 1)
	command = exec.Command("jq", "-c", "-e", "--arg", "scale", "medium", "--argjson", "node_id", "2", "-f", program)
	command.Stdin = strings.NewReader(missing)
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "missing effective config WK_CLUSTER_CHANNEL_RPC_WORKERS") {
		t.Fatalf("missing runtime key error = %v output=%s", err, output)
	}

	for _, testCase := range []struct {
		name  string
		input string
	}{
		{name: "wrong node", input: strings.Replace(input, `"node_id":2`, `"node_id":3`, 1)},
		{name: "wrong source", input: strings.Replace(input, `"source":"effective_startup_config"`, `"source":"live_config"`, 1)},
		{name: "restart not required", input: strings.Replace(input, `"requires_restart":true`, `"requires_restart":false`, 1)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command := exec.Command("jq", "-c", "-e", "--arg", "scale", "medium", "--argjson", "node_id", "2", "-f", program)
			command.Stdin = strings.NewReader(testCase.input)
			if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "invalid effective startup config identity") {
				t.Fatalf("identity mismatch error = %v output=%s", err, output)
			}
		})
	}
}
