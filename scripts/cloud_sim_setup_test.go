package scripts_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

func TestCloudSimulationSetupConfiguresAlibabaAndGitHubWithoutAPIKey(t *testing.T) {
	runTimingSensitiveShellScriptTestExclusively(t)
	root := repoRoot(t)
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	callLog := filepath.Join(temp, "calls.log")
	capturedConfig := filepath.Join(temp, "bootstrap.json")
	bootstrapState := filepath.Join(temp, "bootstrap-applied")
	configHome := filepath.Join(temp, "config-home")
	githubState := filepath.Join(temp, "github-state")
	writeSetupExecutable(t, filepath.Join(bin, "aliyun"), `#!/usr/bin/env bash
set -euo pipefail
printf 'aliyun %s\n' "$*" >>"$WK_SETUP_CALL_LOG"
if [[ -n "${WK_SETUP_HANG_ALIYUN_MATCH:-}" && "$*" == "$WK_SETUP_HANG_ALIYUN_MATCH" ]]; then
	/bin/bash -c 'trap "" TERM; printf "%s\n" "$$" >"$WK_SETUP_STUBBORN_PID"; while :; do /bin/sleep 1; done' &
	: >"$WK_SETUP_HANG_READY"
	trap '' TERM
	wait
fi
case "$1 $2" in
	"configure get")
		printf '%s\n' '{"region_id":"cn-hangzhou"}'
		;;
  "sts GetCallerIdentity")
    printf '%s\n' '{"AccountId":"1234567890123456"}'
    ;;
	"ecs DescribeRegions")
		printf '%s\n' '{"Regions":{"Region":[{"RegionId":"cn-hangzhou"},{"RegionId":"cn-shanghai"}]}}'
		;;
  "ecs DescribeZones")
    printf '%s\n' '{"Zones":{"Zone":[{"ZoneId":"cn-hangzhou-h","ZoneType":"AvailabilityZone","AvailableDiskCategories":{"DiskCategories":["cloud_essd"]},"AvailableInstanceTypes":{"InstanceTypes":[]}}]}}'
    ;;
  "ecs DescribeImages")
    printf '%s\n' '{"Images":{"Image":[{"ImageId":"aliyun_3_x64_20G_alibase_20260513.vhd","CreationTime":"2026-05-13T00:00:00Z","IsSupportCloudinit":true}]}}'
    ;;
	"ecs DescribeInstanceTypes")
		case " $* " in
			*" --MinimumCpuCoreCount 2 "*" --NextToken page-2 "*) type=ecs.g8i.large; cpu=2; memory=4 ;;
			*" --MinimumCpuCoreCount 2 "*)
				printf '%s\n' '{"NextToken":"page-2","InstanceTypes":{"InstanceType":[{"InstanceTypeId":"ecs.g8y.large","CpuArchitecture":"ARM","GPUAmount":0,"InstanceFamilyLevel":"EnterpriseLevel","EniPrivateIpAddressQuantity":8}]}}'
				exit 0
				;;
			*" --MinimumCpuCoreCount 4 "*) type=ecs.g8i.xlarge; cpu=4; memory=8 ;;
      *" --MinimumCpuCoreCount 8 "*) type=ecs.g8i.2xlarge; cpu=8; memory=16 ;;
      *) exit 97 ;;
    esac
		jq -cn --arg type "$type" --argjson cpu "$cpu" --argjson memory "$memory" '
			{InstanceTypes:{InstanceType:
				([range(0;13) | {InstanceTypeId:("ecs.g9z." + tostring),CpuArchitecture:"X86",GPUAmount:0,InstanceFamilyLevel:"EnterpriseLevel",EniPrivateIpAddressQuantity:8}]
				+ [{InstanceTypeId:"ecs.gn7i-c8g1.2xlarge",CpuArchitecture:"X86",GPUAmount:1,InstanceFamilyLevel:"EnterpriseLevel",EniPrivateIpAddressQuantity:8},
				   {InstanceTypeId:"ecs.g8y.large",CpuArchitecture:"ARM",GPUAmount:0,InstanceFamilyLevel:"EnterpriseLevel",EniPrivateIpAddressQuantity:8},
				   {InstanceTypeId:$type,CpuArchitecture:"X86",GPUAmount:0,InstanceFamilyLevel:"EnterpriseLevel",CpuCoreCount:$cpu,MemorySize:$memory,EniPrivateIpAddressQuantity:8}])}}'
    ;;
  "ecs DescribeAvailableResource")
    instance_type=""
    while (($#)); do
      if [[ "$1" == "--InstanceType" ]]; then instance_type="$2"; break; fi
      shift
    done
		status=Available
		if [[ "$instance_type" == ecs.g9z.* ]]; then status=SoldOut; fi
		printf '{"AvailableZones":{"AvailableZone":[{"ZoneId":"cn-hangzhou-h","Status":"Available","AvailableResources":{"AvailableResource":[{"SupportedResources":{"SupportedResource":[{"Value":"%s","Status":"%s"}]}}]}}]}}\n' "$instance_type" "$status"
    ;;
  *) exit 96 ;;
esac
`)
	writeSetupExecutable(t, filepath.Join(bin, "gh"), `#!/usr/bin/env bash
set -euo pipefail
printf 'gh %s\n' "$*" >>"$WK_SETUP_CALL_LOG"
if [[ "${1:-}" == --version ]]; then
	printf '%s\n' 'gh version 2.96.0 (test)'
	exit 0
fi
mkdir -p "$WK_SETUP_GH_STATE"
case "$1" in
  auth) exit 0 ;;
  repo)
    case " $* " in
      *" defaultBranchRef "*) printf '%s\n' main ;;
      *) printf '%s\n' example/project ;;
    esac
    ;;
	variable)
		name="$3"
		environment=repository
		for ((index=1; index <= $#; index++)); do
			if [[ "${!index}" == "--env" ]]; then
				next=$((index + 1))
				environment="${!next}"
			fi
		done
		cat >"$WK_SETUP_GH_STATE/variable_${environment}_${name}"
		;;
	secret)
		name="$3"
		environment=""
		for ((index=1; index <= $#; index++)); do
			if [[ "${!index}" == "--env" ]]; then
				next=$((index + 1))
				environment="${!next}"
			fi
		done
		cat >/dev/null
		: >"$WK_SETUP_GH_STATE/secret_${environment}_${name}"
		;;
  api)
		method=GET
		endpoint=""
		previous=""
		for argument in "$@"; do
			if [[ "$previous" == "--method" ]]; then method="$argument"; fi
			if [[ "$argument" == repos/* ]]; then endpoint="$argument"; fi
			previous="$argument"
		done
		if [[ " $* " == *" --input - "* ]]; then cat >/dev/null; fi
		case "$endpoint" in
			repos/example/project)
				printf '%s\n' '{"permissions":{"admin":true}}'
				;;
			repos/example/project/environments/*/variables/*)
				environment="${endpoint#repos/example/project/environments/}"
				environment="${environment%%/*}"
				name="${endpoint##*/}"
				value="$(cat "$WK_SETUP_GH_STATE/variable_${environment}_${name}")"
				if [[ "${WK_SETUP_CORRUPT_VARIABLE:-}" == "$name" ]]; then value=wrong; fi
				jq -cn --arg name "$name" --arg value "$value" '{name:$name,value:$value}'
				;;
			repos/example/project/actions/variables/*)
				name="${endpoint##*/}"
				value="$(cat "$WK_SETUP_GH_STATE/variable_repository_${name}")"
				if [[ "${WK_SETUP_CORRUPT_VARIABLE:-}" == "$name" ]]; then value=wrong; fi
				jq -cn --arg name "$name" --arg value "$value" '{name:$name,value:$value}'
				;;
			repos/example/project/environments/*/secrets/*)
				environment="${endpoint#repos/example/project/environments/}"
				environment="${environment%%/*}"
				name="${endpoint##*/}"
				if [[ -f "$WK_SETUP_GH_STATE/secret_${environment}_${name}" ]]; then
					jq -cn --arg name "$name" '{name:$name}'
				else
					printf '%s\n' 'gh: Not Found (HTTP 404)' >&2
					exit 1
				fi
				;;
			repos/example/project/environments/*)
				environment="${endpoint##*/}"
				if [[ "$method" == PUT ]]; then
					: >"$WK_SETUP_GH_STATE/environment_${environment}"
					printf '%s\n' '{}'
				elif [[ "${WK_SETUP_ENVIRONMENT_GET_ERROR:-}" == 403 ]]; then
					printf '%s\n' 'gh: Forbidden (HTTP 403)' >&2
					exit 1
				elif [[ -f "$WK_SETUP_GH_STATE/environment_${environment}" ]]; then
					printf '{"name":"%s","deployment_branch_policy":{"protected_branches":true}}\n' "$environment"
				else
					printf '%s\n' 'gh: Not Found (HTTP 404)' >&2
					exit 1
				fi
				;;
			repos/example/project/actions/oidc/customization/sub)
				if [[ "$method" == PUT ]]; then
					printf '%s\n' '{}'
				else
					printf '%s\n' '{"use_default":false,"use_immutable_subject":false,"include_claim_keys":["repo","context","job_workflow_ref"]}'
				fi
				;;
			repos/example/project/commits/main)
				printf '%s\n' 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
				;;
			*) printf '%s\n' '{}' ;;
		esac
		;;
	workflow)
		current="$(cat "$WK_SETUP_GH_STATE/run-id" 2>/dev/null || printf '%s' 100)"
		printf '%s' "$((current + 1))" >"$WK_SETUP_GH_STATE/run-id"
		for argument in "$@"; do
			if [[ "$argument" == verification_id=* ]]; then
				printf '%s' "${argument#verification_id=}" >"$WK_SETUP_GH_STATE/verification-id"
			fi
		done
		;;
	run)
		case "$2" in
			list)
				current="$(cat "$WK_SETUP_GH_STATE/run-id" 2>/dev/null || printf '%s' 100)"
				verification_id="$(cat "$WK_SETUP_GH_STATE/verification-id" 2>/dev/null || true)"
				jq -cn --argjson current "$current" --arg verification_id "$verification_id" '
					[{databaseId:999,displayTitle:"Cloud Simulation OIDC Verification another-setup"},
					 {databaseId:$current,displayTitle:("Cloud Simulation OIDC Verification " + $verification_id)}]'
				;;
			view) printf '%s\n' '{"status":"completed","conclusion":"success"}' ;;
			*) exit 94 ;;
		esac
    ;;
  *) exit 95 ;;
esac
`)
	writeSetupExecutable(t, filepath.Join(bin, "go"), `#!/usr/bin/env bash
set -euo pipefail
printf 'go %s\n' "$*" >>"$WK_SETUP_CALL_LOG"
if [[ "$*" == "env GOVERSION" ]]; then
	printf '%s\n' go1.25.11
	exit 0
fi
if [[ "$*" == "env GOROOT" ]]; then
	(cd "$(dirname "$0")/.." && pwd)
	exit 0
fi
if [[ "${1:-}" == run ]]; then
	expected_root="$(cd "$(dirname "$0")/.." && pwd)"
	if [[ "${GOTOOLCHAIN:-}" != local || "${GOROOT:-}" != "$expected_root" || "${GOWORK:-}" != off ]]; then
		printf 'unpinned go environment: GOROOT=%s GOTOOLCHAIN=%s GOWORK=%s\n' \
			"${GOROOT:-}" "${GOTOOLCHAIN:-}" "${GOWORK:-}" >&2
		exit 93
	fi
fi
config=""
operation=""
while (($#)); do
  case "$1" in
    --config) config="$2"; shift 2 ;;
    plan|apply) operation="$1"; shift ;;
    *) shift ;;
  esac
done
cp "$config" "$WK_SETUP_CAPTURE_CONFIG"
if [[ -n "${WK_SETUP_HANG_GO_OPERATION:-}" && "$operation" == "$WK_SETUP_HANG_GO_OPERATION" ]]; then
	/bin/bash -c 'trap "" TERM; printf "%s\n" "$$" >"$WK_SETUP_STUBBORN_PID"; while :; do /bin/sleep 1; done' &
	: >"$WK_SETUP_HANG_READY"
	trap '' TERM
	wait
fi
case "$operation" in
  plan)
    if [[ -f "$WK_SETUP_BOOTSTRAP_STATE" ]]; then
      printf '%s\n' '{"changes":[]}'
    else
      printf '%s\n' '{"changes":[{"resource":"oidc_provider","action":"create"}]}'
    fi
    ;;
  apply)
    : >"$WK_SETUP_BOOTSTRAP_STATE"
    printf '%s\n' '{"account_id":"1234567890123456","account_id_hash":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","region":"cn-hangzhou","oidc_provider_arn":"acs:ram::1234567890123456:oidc-provider/wukongim-github","provisioner_role_arn":"acs:ram::1234567890123456:role/wukongim-cloud-sim-provisioner","analyzer_role_arn":"acs:ram::1234567890123456:role/wukongim-cloud-sim-analyzer","oidc_audience":"wukongim-cloud-sim"}'
    ;;
  *) exit 94 ;;
esac
`)

	command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "setup.sh"),
		"--region", "cn-hangzhou", "--repository", "example/project", "--yes")
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"WK_SETUP_CALL_LOG="+callLog,
		"WK_SETUP_CAPTURE_CONFIG="+capturedConfig,
		"WK_SETUP_BOOTSTRAP_STATE="+bootstrapState,
		"WK_SETUP_GH_STATE="+githubState,
		"XDG_CONFIG_HOME="+configHome,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		calls, _ := os.ReadFile(callLog)
		t.Fatalf("setup: %v\n%s\ncalls:\n%s", err, output, calls)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	callText := string(calls)
	if strings.Contains(callText, "gh secret set") || strings.Contains(callText, "OPENAI_API_KEY") {
		t.Fatalf("setup unexpectedly configured API credentials:\n%s", calls)
	}
	for _, fragment := range []string{
		"aliyun configure get",
		"aliyun sts GetCallerIdentity",
		"aliyun ecs DescribeRegions",
		"aliyun ecs DescribeZones",
		"aliyun ecs DescribeImages",
		"aliyun ecs DescribeInstanceTypes --MinimumCpuCoreCount 2 --MaximumCpuCoreCount 2 --MinimumMemorySize 4 --MaximumMemorySize 4 --MaxResults 100 --NextToken page-2",
		"gh api repos/example/project/environments/cloud-sim-provision",
		"gh api --method PUT repos/example/project/environments/cloud-sim-provision",
		"gh api --method PUT repos/example/project/environments/cloud-sim-cleanup",
		"gh api --method PUT repos/example/project/environments/cloud-sim-analysis",
		"gh variable set ALIBABA_CLOUD_SIM_CONFIG_JSON --repo example/project",
		"gh variable set ALIBABA_CLOUD_SIM_PROVISIONER_ROLE_ARN --env cloud-sim-cleanup --repo example/project",
		"gh variable set ALIBABA_CLOUD_SIM_ANALYZER_ROLE_ARN --env cloud-sim-analysis --repo example/project",
		"gh api repos/example/project/actions/variables/ALIBABA_CLOUD_SIM_CONFIG_JSON",
		"gh api repos/example/project/environments/cloud-sim-analysis/variables/ALIBABA_CLOUD_SIM_ANALYZER_ROLE_ARN",
		"gh api --method PUT repos/example/project/actions/oidc/customization/sub",
		"gh workflow run cloud-sim-oidc-subject.yml --repo example/project --ref main",
		"-f verification_id=setup-92324335-",
		"gh run view 101 --repo example/project --json status,conclusion",
	} {
		if !strings.Contains(callText, fragment) {
			t.Fatalf("setup calls missing %q:\n%s", fragment, calls)
		}
	}
	if count := strings.Count(callText, "go run ./cmd/wkcloudbootstrap"); count != 3 {
		t.Fatalf("bootstrap invocation count = %d, want plan/apply/plan:\n%s", count, calls)
	}

	configBytes, err := os.ReadFile(capturedConfig)
	if err != nil {
		t.Fatalf("read captured config: %v", err)
	}
	var config struct {
		Bootstrap struct {
			AccountID           string `json:"account_id"`
			Region              string `json:"region"`
			Repository          string `json:"repository"`
			OIDCProviderName    string `json:"oidc_provider_name"`
			ProvisionerRoleName string `json:"provisioner_role_name"`
			AnalyzerRoleName    string `json:"analyzer_role_name"`
		} `json:"bootstrap"`
		Provider struct {
			ZoneID  string `json:"zone_id"`
			ImageID string `json:"image_id"`
			Presets map[string]struct {
				InstanceTypes []string `json:"instance_types"`
			} `json:"presets"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(configBytes, &config); err != nil {
		t.Fatalf("decode captured config: %v\n%s", err, configBytes)
	}
	if config.Bootstrap.AccountID != "1234567890123456" || config.Bootstrap.Region != "cn-hangzhou" || config.Bootstrap.Repository != "example/project" {
		t.Fatalf("bootstrap config = %#v", config.Bootstrap)
	}
	if config.Bootstrap.OIDCProviderName != "wukongim-github-92324335" ||
		config.Bootstrap.ProvisionerRoleName != "wukongim-cloud-sim-provisioner-92324335" ||
		config.Bootstrap.AnalyzerRoleName != "wukongim-cloud-sim-analyzer-92324335" {
		t.Fatalf("repository-scoped bootstrap names = %#v", config.Bootstrap)
	}
	if config.Provider.ZoneID != "cn-hangzhou-h" || config.Provider.ImageID != "aliyun_3_x64_20G_alibase_20260513.vhd" {
		t.Fatalf("provider selection = %#v", config.Provider)
	}
	for preset, want := range map[string]string{"small": "ecs.g8i.large", "standard": "ecs.g8i.xlarge", "stress": "ecs.g8i.2xlarge"} {
		if got := config.Provider.Presets[preset].InstanceTypes; len(got) != 1 || got[0] != want {
			t.Fatalf("preset %s = %v, want %s", preset, got, want)
		}
	}
	persistedConfig := filepath.Join(configHome, "wukongim", "cloud-sim", "example_project", "bootstrap.json")
	info, err := os.Stat(persistedConfig)
	if err != nil {
		t.Fatalf("stat persisted bootstrap config: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("persisted bootstrap mode = %o, want 600", info.Mode().Perm())
	}

	runHangingSetup := func(t *testing.T, extraEnv ...string) (string, string, time.Duration) {
		t.Helper()
		if err := os.Remove(bootstrapState); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		before, err := os.ReadFile(callLog)
		if err != nil {
			t.Fatal(err)
		}
		ready := filepath.Join(t.TempDir(), "hang-ready")
		stubbornPID := filepath.Join(t.TempDir(), "stubborn-pid")
		defer terminateSetupFixtureProcess(stubbornPID)
		// The script's own one-second bound is the behavior under test. Keep the
		// outer harness deadline wide enough that a CPU-starved CI runner can reap
		// the TERM-ignoring fixture before ExecCommandContext becomes the failure.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "bash", filepath.Join(root, "scripts", "cloud-sim", "setup.sh"),
			"--region", "cn-hangzhou", "--repository", "example/project", "--yes")
		command.WaitDelay = 500 * time.Millisecond
		command.Dir = root
		command.Env = append(os.Environ(),
			"PATH="+bin+":"+os.Getenv("PATH"),
			"WK_SETUP_CALL_LOG="+callLog,
			"WK_SETUP_CAPTURE_CONFIG="+capturedConfig,
			"WK_SETUP_BOOTSTRAP_STATE="+bootstrapState,
			"WK_SETUP_GH_STATE="+githubState,
			"WK_SETUP_HANG_READY="+ready,
			"WK_SETUP_STUBBORN_PID="+stubbornPID,
			"WK_SETUP_COMMAND_TIMEOUT_SECONDS=1",
			"XDG_CONFIG_HOME="+configHome,
		)
		command.Env = append(command.Env, extraEnv...)
		started := time.Now()
		output, runErr := command.CombinedOutput()
		elapsed := time.Since(started)
		if ctx.Err() != nil {
			t.Fatalf("setup did not bound the hanging command: %v\n%s", ctx.Err(), output)
		}
		if runErr == nil {
			t.Fatalf("setup unexpectedly continued after a hanging command:\n%s", output)
		}
		if _, err := os.Stat(ready); err != nil {
			t.Fatalf("setup did not reach the hanging fixture: %v\n%s", err, output)
		}
		if elapsed > 8*time.Second {
			t.Fatalf("bounded setup took %s, want under 8s\n%s", elapsed, output)
		}
		if !waitForSetupFixtureProcessExit(stubbornPID, 2*time.Second) {
			t.Fatalf("bounded setup leaked its TERM-ignoring descendant (pid file %s)", stubbornPID)
		}
		after, err := os.ReadFile(callLog)
		if err != nil {
			t.Fatal(err)
		}
		return string(output), string(after[len(before):]), elapsed
	}

	t.Run("bounds Alibaba CLI and kills its process tree before mutation", func(t *testing.T) {
		output, calls, _ := runHangingSetup(t, "WK_SETUP_HANG_ALIYUN_MATCH=sts GetCallerIdentity")
		if !strings.Contains(output, "Alibaba CLI command timed out") {
			t.Fatalf("setup did not explain the Alibaba timeout:\n%s", output)
		}
		assertNoSetupMutation(t, calls)
		if strings.Contains(calls, "go run ./cmd/wkcloudbootstrap") {
			t.Fatalf("setup reached the Alibaba OIDC/RAM bootstrap after an inventory timeout:\n%s", calls)
		}
	})

	t.Run("bounds bootstrap apply and reports uncertain state before GitHub mutation", func(t *testing.T) {
		output, calls, _ := runHangingSetup(t, "WK_SETUP_HANG_GO_OPERATION=apply")
		if !strings.Contains(output, "bootstrap apply timed out") ||
			!strings.Contains(output, "state may be uncertain") ||
			!strings.Contains(output, "rerun setup to obtain a fresh plan") {
			t.Fatalf("setup did not explain uncertain apply state and recovery:\n%s", output)
		}
		assertNoSetupMutation(t, calls)
	})

	t.Run("does not overwrite an environment after a read error", func(t *testing.T) {
		before, err := os.ReadFile(callLog)
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "setup.sh"),
			"--region", "cn-hangzhou", "--repository", "example/project", "--yes")
		command.Dir = root
		command.Env = append(os.Environ(),
			"PATH="+bin+":"+os.Getenv("PATH"),
			"WK_SETUP_CALL_LOG="+callLog,
			"WK_SETUP_CAPTURE_CONFIG="+capturedConfig,
			"WK_SETUP_BOOTSTRAP_STATE="+bootstrapState,
			"WK_SETUP_GH_STATE="+githubState,
			"WK_SETUP_ENVIRONMENT_GET_ERROR=403",
			"XDG_CONFIG_HOME="+configHome,
		)
		output, err := command.CombinedOutput()
		if err == nil {
			t.Fatalf("setup unexpectedly ignored environment read failure:\n%s", output)
		}
		if !strings.Contains(string(output), "cannot inspect GitHub environment") {
			t.Fatalf("setup error does not explain environment read failure:\n%s", output)
		}
		after, err := os.ReadFile(callLog)
		if err != nil {
			t.Fatal(err)
		}
		newCalls := string(after[len(before):])
		if strings.Contains(newCalls, "--method PUT repos/example/project/environments/") {
			t.Fatalf("setup overwrote an environment after a 403:\n%s", newCalls)
		}
	})

	t.Run("fails when a written variable cannot be verified", func(t *testing.T) {
		command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "setup.sh"),
			"--region", "cn-hangzhou", "--repository", "example/project", "--yes")
		command.Dir = root
		command.Env = append(os.Environ(),
			"PATH="+bin+":"+os.Getenv("PATH"),
			"WK_SETUP_CALL_LOG="+callLog,
			"WK_SETUP_CAPTURE_CONFIG="+capturedConfig,
			"WK_SETUP_BOOTSTRAP_STATE="+bootstrapState,
			"WK_SETUP_GH_STATE="+githubState,
			"WK_SETUP_CORRUPT_VARIABLE=ALIBABA_CLOUD_SIM_CONFIG_JSON",
			"XDG_CONFIG_HOME="+configHome,
		)
		output, err := command.CombinedOutput()
		if err == nil {
			t.Fatalf("setup unexpectedly ignored variable verification failure:\n%s", output)
		}
		if !strings.Contains(string(output), "GitHub variable verification failed") {
			t.Fatalf("setup error does not explain variable verification failure:\n%s", output)
		}
	})
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

func TestCloudSimulationSetupRejectsInvalidCommandTimeoutBeforeToolUse(t *testing.T) {
	root := repoRoot(t)
	callLog := filepath.Join(t.TempDir(), "tool-used")
	command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "setup.sh"), "--yes")
	command.Dir = root
	command.Env = append(os.Environ(),
		"WK_SETUP_COMMAND_TIMEOUT_SECONDS=0",
		"WK_SETUP_CALL_LOG="+callLog,
	)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "WK_SETUP_COMMAND_TIMEOUT_SECONDS must be a positive integer") {
		t.Fatalf("setup did not reject its invalid command timeout early: %v\n%s", err, output)
	}
	if _, err := os.Stat(callLog); !os.IsNotExist(err) {
		t.Fatalf("setup used a tool before validating its timeout: %v", err)
	}
}

func TestCloudSimulationSetupExitsOnTerminationBeforeMutation(t *testing.T) {
	root := repoRoot(t)
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(temp, "go-ready")
	mutation := filepath.Join(temp, "mutation")
	for _, name := range []string{"aliyun", "curl", "git", "jq", "tar"} {
		writeSetupExecutable(t, filepath.Join(bin, name), "#!/usr/bin/env bash\nexit 0\n")
	}
	writeSetupExecutable(t, filepath.Join(bin, "go"), `#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == "env GOVERSION" ]]; then
  : >"$WK_SETUP_SIGNAL_READY"
  /bin/sleep 1
  printf '%s\n' go1.25.11
  exit 0
fi
if [[ "$*" == "env GOROOT" ]]; then
  (cd "$(dirname "$0")/.." && pwd)
  exit 0
fi
exit 92
`)
	writeSetupExecutable(t, filepath.Join(bin, "gh"), `#!/usr/bin/env bash
: >"$WK_SETUP_SIGNAL_MUTATION"
if [[ "${1:-}" == --version ]]; then printf '%s\n' 'gh version 2.96.0 (test)'; fi
exit 91
`)

	command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "setup.sh"), "--yes")
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"WK_SETUP_SIGNAL_READY="+ready,
		"WK_SETUP_SIGNAL_MUTATION="+mutation,
		"XDG_CACHE_HOME="+filepath.Join(temp, "cache"),
	)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatal("setup did not reach the bounded Go probe")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	err := command.Wait()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 130 {
		t.Fatalf("setup interrupt exit = %v, want 130", err)
	}
	if _, err := os.Stat(mutation); !os.IsNotExist(err) {
		t.Fatalf("setup continued into GitHub mutation after interrupt: %v", err)
	}
}

func TestCloudSimulationSetupBoundsAndResumesGitHubCLIDownload(t *testing.T) {
	runTimingSensitiveShellScriptTestExclusively(t)
	root := repoRoot(t)
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	systemBin := filepath.Join(temp, "system-bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(systemBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSetupExecutable(t, filepath.Join(systemBin, "gh"), `#!/usr/bin/env bash
if [[ "${1:-}" == --version ]]; then
  printf '%s\n' 'gh version 1.0.0 (stale)'
  exit 0
fi
printf '%s\n' SYSTEM_GH_USED >&2
exit 78
`)

	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		t.Skipf("unsupported test architecture %s", arch)
	}
	archive := filepath.Join(temp, "fake-gh.tar.gz")
	writeSetupTarGz(t, archive, "gh_2.96.0_linux_"+arch+"/bin/gh", []byte(`#!/usr/bin/env bash
if [[ "${1:-}" == --version ]]; then
  printf '%s\n' 'gh version 2.96.0 (test)'
  exit 0
fi
printf '%s\n' GH_FAKE_REACHED >&2
exit 77
`), 0o755)

	callLog := filepath.Join(temp, "calls.log")
	for _, command := range []string{"aliyun", "git", "jq"} {
		writeSetupExecutable(t, filepath.Join(bin, command), "#!/usr/bin/env bash\nexit 0\n")
	}
	writeSetupExecutable(t, filepath.Join(bin, "go"), `#!/usr/bin/env bash
if [[ "$*" == "env GOVERSION" ]]; then printf '%s\n' go1.25.11; exit 0; fi
if [[ "$*" == "env GOROOT" ]]; then (cd "$(dirname "$0")/.." && pwd); exit 0; fi
exit 0
`)
	writeSetupExecutable(t, filepath.Join(bin, "sha256sum"), `#!/usr/bin/env bash
case "$1" in
  *linux_amd64.tar.gz*) printf '%s  %s\n' 83d5c2ccad5498f58bf6368acb1ab32588cf43ab3a4b1c301bf36328b1c8bd60 "$1" ;;
  *linux_arm64.tar.gz*) printf '%s  %s\n' 06f86ec7103d41993b76cd78072f43595c34aaa56506d971d9860e67140bf909 "$1" ;;
  *) printf '%064d  %s\n' 0 "$1" ;;
esac
`)
	writeSetupExecutable(t, filepath.Join(bin, "curl"), `#!/usr/bin/env bash
set -euo pipefail
printf 'curl %s\n' "$*" >>"$WK_SETUP_CALL_LOG"
for required in '--ipv4' '--http1.1' '--connect-timeout 8' '--speed-time 15' '--speed-limit 1024' '--retry-max-time 90' '--continue-at -'; do
  if [[ " $* " != *" $required "* ]]; then
    printf 'missing bounded download option: %s\n' "$required" >&2
    exit 28
  fi
done
output=""
while (($#)); do
  if [[ "$1" == --output ]]; then output="$2"; break; fi
  shift
done
test -n "$output"
cp "$WK_SETUP_FAKE_GH_ARCHIVE" "$output"
exit 28
`)

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", filepath.Join(root, "scripts", "cloud-sim", "setup.sh"), "--yes")
	command.WaitDelay = time.Second
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin+":"+systemBin+":/usr/bin:/bin",
		"WK_SETUP_CALL_LOG="+callLog,
		"WK_SETUP_FAKE_GH_ARCHIVE="+archive,
		"XDG_CACHE_HOME="+filepath.Join(temp, "cache"),
	)
	started := time.Now()
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("bounded download harness exceeded its 12 second deadline: %v\n%s", ctx.Err(), output)
	}
	if err == nil || !strings.Contains(string(output), "GH_FAKE_REACHED") {
		t.Fatalf("setup did not reach the checksum-verified fake gh after a bounded download: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "SYSTEM_GH_USED") {
		t.Fatalf("setup used a preinstalled GitHub CLI instead of the checksum-verified fake archive:\n%s", output)
	}
	if elapsed := time.Since(started); elapsed > 8*time.Second {
		t.Fatalf("bounded download harness took %s", elapsed)
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	callText := string(calls)
	if !strings.Contains(callText, "github.com/cli/cli/releases/download") {
		t.Fatalf("setup did not use the pinned GitHub CLI release: %s", calls)
	}
	if !strings.Contains(callText, filepath.Join(temp, "cache", "wukongim-cloud-sim", "downloads")) {
		t.Fatalf("setup did not retain the resumable download under the user cache: %s", calls)
	}
}

func TestCloudSimulationSetupReplacesStaleGoWithPinnedArchive(t *testing.T) {
	root := repoRoot(t)
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	systemBin := filepath.Join(temp, "system-bin")
	for _, directory := range []string{bin, systemBin} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeSetupExecutable(t, filepath.Join(systemBin, "go"), `#!/usr/bin/env bash
if [[ "$*" == "env GOVERSION" ]]; then printf '%s\n' go1.24.0; exit 0; fi
printf '%s\n' SYSTEM_GO_USED >&2
exit 78
`)
	partialGo := filepath.Join(temp, "cache", "wukongim-cloud-sim", "go1.25.11", "go", "bin", "go")
	if err := os.MkdirAll(filepath.Dir(partialGo), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSetupExecutable(t, partialGo, `#!/usr/bin/env bash
if [[ "$*" == "env GOVERSION" ]]; then printf '%s\n' go1.25.11; exit 0; fi
printf '%s\n' PARTIAL_GO_USED >&2
exit 79
`)
	writeSetupExecutable(t, filepath.Join(bin, "gh"), `#!/usr/bin/env bash
if [[ "${1:-}" == --version ]]; then printf '%s\n' 'gh version 2.96.0 (test)'; exit 0; fi
printf '%s\n' GH_STOP_AFTER_TOOL_SETUP >&2
exit 77
`)
	for _, name := range []string{"aliyun", "git", "jq"} {
		writeSetupExecutable(t, filepath.Join(bin, name), "#!/usr/bin/env bash\nexit 0\n")
	}

	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		t.Skipf("unsupported test architecture %s", arch)
	}
	archive := filepath.Join(temp, "fake-go.tar.gz")
	writeSetupGoTarGz(t, archive, []byte(`#!/usr/bin/env bash
if [[ "$*" == "env GOVERSION" ]]; then
  printf '%s\n' PINNED_GO_VERSION_CHECK >&2
  printf '%s\n' go1.25.11
  exit 0
fi
if [[ "$*" == "env GOROOT" ]]; then
  (cd "$(dirname "$0")/.." && pwd)
  exit 0
fi
printf '%s\n' PINNED_GO_USED >&2
exit 76
`))
	writeSetupExecutable(t, filepath.Join(bin, "sha256sum"), `#!/usr/bin/env bash
case "$1" in
  *linux-amd64.tar.gz) printf '%s  %s\n' 34f14304e856893f4ba30c2cacfe93906e9de7915c5f6aaaf3a81cdccd7ba30b "$1" ;;
  *linux-arm64.tar.gz) printf '%s  %s\n' c30bf9e156a54ea4e31fbbbf31a712b32734b58cc9a22426fa5ee632d0885124 "$1" ;;
  *) printf '%064d  %s\n' 0 "$1" ;;
esac
`)
	callLog := filepath.Join(temp, "calls.log")
	writeSetupExecutable(t, filepath.Join(bin, "curl"), `#!/usr/bin/env bash
set -euo pipefail
printf 'curl %s\n' "$*" >>"$WK_SETUP_CALL_LOG"
output=""
while (($#)); do
  if [[ "$1" == --output ]]; then output="$2"; break; fi
  shift
done
test -n "$output"
cp "$WK_SETUP_FAKE_GO_ARCHIVE" "$output"
`)

	// This context is only the test-harness watchdog. The setup script keeps
	// its own per-command deadlines; allow archive extraction and process
	// startup enough slack when the scripts package is running in parallel.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", filepath.Join(root, "scripts", "cloud-sim", "setup.sh"), "--yes")
	command.WaitDelay = time.Second
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin+":"+systemBin+":/usr/bin:/bin",
		"WK_SETUP_CALL_LOG="+callLog,
		"WK_SETUP_FAKE_GO_ARCHIVE="+archive,
		"XDG_CACHE_HOME="+filepath.Join(temp, "cache"),
	)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("stale Go replacement exceeded deadline: %v\n%s", ctx.Err(), output)
	}
	if err == nil || !strings.Contains(string(output), "GH_STOP_AFTER_TOOL_SETUP") {
		t.Fatalf("setup did not replace stale Go before continuing: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "SYSTEM_GO_USED") {
		t.Fatalf("setup executed stale Go for repository work:\n%s", output)
	}
	if strings.Contains(string(output), "PARTIAL_GO_USED") {
		t.Fatalf("setup trusted a partial permanent Go cache:\n%s", output)
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(calls), "mirrors.aliyun.com/golang/go1.25.11") {
		t.Fatalf("setup did not download the pinned Go archive from the preferred mirror:\n%s", calls)
	}
}

func TestCloudSimulationSetupRejectsChecksumMismatch(t *testing.T) {
	root := repoRoot(t)
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	systemBin := filepath.Join(temp, "system-bin")
	for _, directory := range []string{bin, systemBin} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeSetupExecutable(t, filepath.Join(bin, "go"), `#!/usr/bin/env bash
if [[ "$*" == "env GOVERSION" ]]; then printf '%s\n' go1.25.11; exit 0; fi
if [[ "$*" == "env GOROOT" ]]; then (cd "$(dirname "$0")/.." && pwd); exit 0; fi
exit 0
`)
	writeSetupExecutable(t, filepath.Join(systemBin, "gh"), `#!/usr/bin/env bash
if [[ "${1:-}" == --version ]]; then printf '%s\n' 'gh version 1.0.0 (stale)'; exit 0; fi
printf '%s\n' CORRUPT_GH_EXECUTED >&2
exit 79
`)
	for _, name := range []string{"aliyun", "git", "jq"} {
		writeSetupExecutable(t, filepath.Join(bin, name), "#!/usr/bin/env bash\nexit 0\n")
	}
	writeSetupExecutable(t, filepath.Join(bin, "sha256sum"), `#!/usr/bin/env bash
printf '%064d  %s\n' 0 "$1"
`)
	writeSetupExecutable(t, filepath.Join(bin, "curl"), `#!/usr/bin/env bash
set -euo pipefail
output=""
while (($#)); do
  if [[ "$1" == --output ]]; then output="$2"; break; fi
  shift
done
test -n "$output"
printf '%s\n' corrupt >"$output"
`)

	// Keep the outer harness from masking the checksum assertion under
	// concurrent process startup; the script's command deadline remains the
	// behavior under test.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", filepath.Join(root, "scripts", "cloud-sim", "setup.sh"), "--yes")
	command.WaitDelay = time.Second
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin+":"+systemBin+":/usr/bin:/bin",
		"XDG_CACHE_HOME="+filepath.Join(temp, "cache"),
	)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("checksum mismatch harness exceeded deadline: %v\n%s", ctx.Err(), output)
	}
	if err == nil || !strings.Contains(string(output), "checksum mismatch") {
		t.Fatalf("setup did not fail closed on a corrupt archive: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "CORRUPT_GH_EXECUTED") {
		t.Fatalf("setup executed a checksum-invalid GitHub CLI:\n%s", output)
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

func writeSetupTarGz(t *testing.T, path, name string, content []byte, mode int64) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertNoSetupMutation(t *testing.T, calls string) {
	t.Helper()
	for _, mutation := range []string{
		"gh variable set",
		"gh secret set",
		"gh api --method PUT",
		"gh workflow run",
	} {
		if strings.Contains(calls, mutation) {
			t.Fatalf("setup continued into mutation after a bounded failure (%s):\n%s", mutation, calls)
		}
	}
}

func waitForSetupFixtureProcessExit(pidPath string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pidBytes, err := os.ReadFile(pidPath)
		if err != nil {
			if os.IsNotExist(err) {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			return false
		}
		pid := strings.TrimSpace(string(pidBytes))
		if pid == "" || exec.Command("/bin/kill", "-0", pid).Run() != nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func terminateSetupFixtureProcess(pidPath string) {
	pidBytes, err := os.ReadFile(pidPath)
	if err != nil {
		return
	}
	pid := strings.TrimSpace(string(pidBytes))
	if pid != "" {
		_ = exec.Command("/bin/kill", "-KILL", pid).Run()
	}
}

func writeSetupGoTarGz(t *testing.T, path string, executable []byte) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	entries := []struct {
		name    string
		content []byte
		mode    int64
	}{
		{name: "go/bin/go", content: executable, mode: 0o755},
		{name: "go/src/runtime/proc.go", content: []byte("package runtime\n"), mode: 0o644},
		{name: "go/pkg/tool/.complete", content: []byte("complete\n"), mode: 0o644},
	}
	for _, entry := range entries {
		if err := tarWriter.WriteHeader(&tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(entry.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeSetupExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}
