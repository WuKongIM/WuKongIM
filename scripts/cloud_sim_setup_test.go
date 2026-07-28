package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudSimulationSetupHelpDescribesOneCommandContract(t *testing.T) {
	script := filepath.Join(repoRoot(t), "scripts", "cloud-sim", "setup.sh")
	command := exec.Command("bash", script, "--help")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("setup --help: %v\n%s", err, output)
	}
	text := string(output)
	for _, fragment := range []string{
		"Usage: ./scripts/cloud-sim/setup.sh",
		"--region",
		"--repository",
		"--yes",
		"WK_SETUP_COMMAND_TIMEOUT_SECONDS",
		"ChatGPT",
		"does not create billable cloud resources",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("setup --help missing %q:\n%s", fragment, text)
		}
	}
}

func TestCloudSimulationSetupRecommendedInputsMatchProvisionWorkflow(t *testing.T) {
	root := repoRoot(t)
	setup, err := os.ReadFile(filepath.Join(root, "scripts", "cloud-sim", "setup.sh"))
	if err != nil {
		t.Fatal(err)
	}
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "cloud-sim-provision.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"scenario=cloud-small",
		"infrastructure_preset=small",
		"duration=30m",
		"analysis_grace=2h",
		"max_total_cost=70",
	} {
		if !strings.Contains(string(setup), required) {
			t.Fatalf("setup recommendation missing %q", required)
		}
	}
	for _, required := range []string{
		"options: [cloud-small, cloud-medium, cloud-large, cloud-standard, cloud-stress]",
		"options: [small, medium, large, standard, stress]",
		"options: [30m, 2h, 24h, 48h, 168h]",
		"options: [2h, 6h]",
		`default: "70"`,
	} {
		if !strings.Contains(string(workflow), required) {
			t.Fatalf("Provision workflow does not admit the setup recommendation contract %q", required)
		}
	}
}

func TestCloudSimulationSetupPinsDownloadedToolchain(t *testing.T) {
	content, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "cloud-sim", "toolchain.env"))
	if err != nil {
		t.Fatalf("read cloud toolchain: %v", err)
	}
	want := []string{
		"GO_VERSION=1.25.11",
		"GO_LINUX_AMD64_SHA256=34f14304e856893f4ba30c2cacfe93906e9de7915c5f6aaaf3a81cdccd7ba30b",
		"GO_LINUX_ARM64_SHA256=c30bf9e156a54ea4e31fbbbf31a712b32734b58cc9a22426fa5ee632d0885124",
		"GH_CLI_VERSION=2.96.0",
		"GH_CLI_LINUX_AMD64_SHA256=83d5c2ccad5498f58bf6368acb1ab32588cf43ab3a4b1c301bf36328b1c8bd60",
		"GH_CLI_LINUX_ARM64_SHA256=06f86ec7103d41993b76cd78072f43595c34aaa56506d971d9860e67140bf909",
	}
	for _, line := range want {
		if !strings.Contains(string(content), line+"\n") {
			t.Fatalf("cloud toolchain missing %q:\n%s", line, content)
		}
	}
	script, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "cloud-sim", "setup.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), "GO_VERSION:-") || strings.Contains(string(script), "GH_CLI_VERSION:-") {
		t.Fatal("setup duplicates toolchain pin fallbacks instead of failing closed on toolchain.env")
	}
	if !strings.Contains(string(script), "https://mirrors.aliyun.com/golang/") {
		t.Fatal("setup does not prefer the Alibaba Go mirror in Alibaba CloudShell")
	}
	if !strings.Contains(string(script), "analysis_grace=2h") || strings.Contains(string(script), "analysis_grace=30m") {
		t.Fatal("setup recommended inputs do not match the Provision workflow analysis-grace choices")
	}
}
