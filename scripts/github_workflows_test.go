package scripts_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type actionPin struct {
	sha     string
	release string
}

type ciWorkflow struct {
	Name        string               `yaml:"name"`
	On          map[string]yaml.Node `yaml:"on"`
	Permissions map[string]string    `yaml:"permissions"`
	Concurrency ciConcurrency        `yaml:"concurrency"`
	Jobs        map[string]ciJob     `yaml:"jobs"`
}

type ciConcurrency struct {
	Group            string `yaml:"group"`
	CancelInProgress *bool  `yaml:"cancel-in-progress"`
}

type ciJob struct {
	Name           string            `yaml:"name"`
	RunsOn         string            `yaml:"runs-on"`
	TimeoutMinutes int               `yaml:"timeout-minutes"`
	Env            map[string]string `yaml:"env"`
	Defaults       *ciDefaults       `yaml:"defaults"`
	Strategy       *ciStrategy       `yaml:"strategy"`
	Steps          []ciStep          `yaml:"steps"`
	Uses           string            `yaml:"uses"`
}

type ciDefaults struct {
	Run ciRunDefaults `yaml:"run"`
}

type ciRunDefaults struct {
	WorkingDirectory string `yaml:"working-directory"`
}

type ciStrategy struct {
	FailFast *bool    `yaml:"fail-fast"`
	Matrix   ciMatrix `yaml:"matrix"`
}

type ciMatrix struct {
	Include []ciMatrixEntry `yaml:"include"`
}

type ciMatrixEntry struct {
	Name     string `yaml:"name"`
	Packages string `yaml:"packages"`
}

type ciStep struct {
	Name  string            `yaml:"name"`
	Uses  string            `yaml:"uses"`
	Run   string            `yaml:"run"`
	Shell string            `yaml:"shell"`
	If    string            `yaml:"if"`
	Env   map[string]string `yaml:"env"`
	With  map[string]any    `yaml:"with"`
}

var approvedActionPins = map[string]actionPin{
	"actions/checkout": {
		sha:     "9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0",
		release: "v7.0.0",
	},
	"actions/setup-go": {
		sha:     "924ae3a1cded613372ab5595356fb5720e22ba16",
		release: "v6.5.0",
	},
	"actions/setup-node": {
		sha:     "249970729cb0ef3589644e2896645e5dc5ba9c38",
		release: "v6.5.0",
	},
	"oven-sh/setup-bun": {
		sha:     "0c5077e51419868618aeaa5fe8019c62421857d6",
		release: "v2.2.0",
	},
	"actions/upload-artifact": {
		sha:     "043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
		release: "v7.0.1",
	},
}

const trackedGoFormattingCommand = `mapfile -t go_files < <(git ls-files '*.go')
test "${#go_files[@]}" -gt 0
unformatted="$(gofmt -l "${go_files[@]}")"
if [[ -n "$unformatted" ]]; then
  echo "The following tracked Go files need gofmt:"
  echo "$unformatted"
  exit 1
fi
`

const embeddedWebBundleCheckCommand = `changes="$(git status --porcelain -- ../internal/access/manager/webui/dist)"
if [[ -n "$changes" ]]; then
  echo "The embedded manager web bundle is stale:"
  echo "$changes"
  exit 1
fi
`

const embeddedDemoBundleCheckCommand = `changes="$(git status --porcelain -- ../../internal/access/api/demoui/dist)"
if [[ -n "$changes" ]]; then
  echo "The embedded chat Demo bundle is stale:"
  echo "$changes"
  exit 1
fi
`

var expectedCIJobs = map[string]ciJob{
	"go-quality": {
		Name:           "Go quality",
		RunsOn:         "ubuntu-24.04",
		TimeoutMinutes: 10,
		Env:            map[string]string{"GOWORK": "off"},
		Steps: []ciStep{
			{
				Uses: "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0",
				With: map[string]any{"persist-credentials": false},
			},
			{
				Uses: "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16",
				With: map[string]any{
					"go-version-file":       "go.mod",
					"cache":                 true,
					"cache-dependency-path": "go.sum",
				},
			},
			{Name: "Verify Go toolchain", Run: `test "$(go env GOVERSION)" = "go1.25.11"`},
			{Name: "Check tracked Go formatting", Shell: "bash", Run: trackedGoFormattingCommand},
			{Name: "Check module metadata", Run: "go mod tidy -diff"},
			{Name: "Vet explicit roots", Run: "go vet ./cmd/... ./internal/... ./pkg/... ./scripts/... ./docker/..."},
		},
	},
	"go-unit": {
		Name:           "Go unit (${{ matrix.name }})",
		RunsOn:         "ubuntu-24.04",
		TimeoutMinutes: 15,
		Env:            map[string]string{"GOWORK": "off"},
		Strategy: &ciStrategy{
			FailFast: boolPointer(false),
			Matrix: ciMatrix{Include: []ciMatrixEntry{
				{Name: "cmd", Packages: "./cmd/..."},
				{Name: "internal", Packages: "./internal/..."},
				{Name: "pkg", Packages: "./pkg/..."},
				{Name: "scripts-docker", Packages: "./scripts/... ./docker/..."},
			}},
		},
		Steps: []ciStep{
			{
				Uses: "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0",
				With: map[string]any{"persist-credentials": false},
			},
			{
				Uses: "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16",
				With: map[string]any{
					"go-version-file":       "go.mod",
					"cache":                 true,
					"cache-dependency-path": "go.sum",
				},
			},
			{Name: "Verify Go toolchain", Run: `test "$(go env GOVERSION)" = "go1.25.11"`},
			{
				Name:  "Run unit group",
				Shell: "bash",
				Env:   map[string]string{"PACKAGES": "${{ matrix.packages }}"},
				Run:   "go test $PACKAGES -count=1 -timeout=14m",
			},
		},
	},
	"web": {
		Name:           "Web",
		RunsOn:         "ubuntu-24.04",
		TimeoutMinutes: 10,
		Defaults:       &ciDefaults{Run: ciRunDefaults{WorkingDirectory: "web"}},
		Steps: []ciStep{
			{
				Uses: "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0",
				With: map[string]any{"persist-credentials": false},
			},
			{
				Uses: "oven-sh/setup-bun@0c5077e51419868618aeaa5fe8019c62421857d6",
				With: map[string]any{"bun-version": "1.3.11"},
			},
			{Name: "Verify Bun version", Run: `test "$(bun --version)" = "1.3.11"`},
			{Name: "Install dependencies", Run: "bun install --frozen-lockfile"},
			{Name: "Lint against baseline", Run: "bun run lint"},
			{Name: "Test", Run: "bun run test"},
			{Name: "Type check", Run: "bunx tsc -b"},
			{Name: "Build", Run: "bun run build"},
			{Name: "Check deterministic tracked build output", Shell: "bash", Run: embeddedWebBundleCheckCommand},
		},
	},
	"demo": {
		Name:           "Demo",
		RunsOn:         "ubuntu-24.04",
		TimeoutMinutes: 10,
		Defaults:       &ciDefaults{Run: ciRunDefaults{WorkingDirectory: "demo/chatdemo"}},
		Steps: []ciStep{
			{
				Uses: "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0",
				With: map[string]any{"persist-credentials": false},
			},
			{
				Uses: "actions/setup-node@249970729cb0ef3589644e2896645e5dc5ba9c38",
				With: map[string]any{"node-version": "22.12.0"},
			},
			{Name: "Enable Corepack", Run: "corepack enable"},
			{Name: "Verify Node version", Run: `test "$(node --version)" = "v22.12.0"`},
			{Name: "Verify Yarn version", Run: `test "$(yarn --version)" = "1.22.22"`},
			{Name: "Install dependencies", Run: "yarn install --frozen-lockfile"},
			{Name: "Test", Run: "yarn test"},
			{Name: "Build", Run: "yarn build"},
			{Name: "Check deterministic tracked build output", Shell: "bash", Run: embeddedDemoBundleCheckCommand},
		},
	},
}

const (
	nightlyRaceCommand = `set -o pipefail
timeout --signal=TERM --kill-after=30s 40m go test -race $PACKAGES -count=1 -timeout=35m -p=1 2>&1 | tee "$LOG_FILE"
`
	nightlyIntegrationCommand = `set -o pipefail
timeout --signal=TERM --kill-after=30s 25m go test -tags=integration ./internal/... ./pkg/... -count=1 -timeout=20m -p=1 2>&1 | tee "$LOG_FILE"
`
	nightlyE2ECommand = `set -o pipefail
WK_E2E_BINARY="$RUNNER_TEMP/wukongim-e2e" timeout --signal=TERM --kill-after=30s 50m go test -tags=e2e ./test/e2e/... -count=1 -timeout=45m -p=1 2>&1 | tee "$LOG_FILE"
`
	nightlyMediumRecipientCommand = `set -o pipefail
timeout --signal=TERM --kill-after=30s 5m \
  go test -tags=e2e ./test/e2e/message/medium_recipient_hotpath \
    -run TestCloudMediumScaledRecipientHotPath -count=1 -timeout=4m -p=1 -v 2>&1 |
  tee "$LOG_FILE"
`
	nightlySmokeCommand = `timeout --signal=TERM --kill-after=30s 25m bash scripts/smoke-wkcli-sim-wukongim-three-nodes.sh \
  --out-dir "$RUNNER_TEMP/$SMOKE_OUT" \
  --ready-timeout 180
`
	nightlySmokeArtifactPaths = `${{ runner.temp }}/${{ env.SMOKE_OUT }}/summary.md
${{ runner.temp }}/${{ env.SMOKE_OUT }}/cluster.log
${{ runner.temp }}/${{ env.SMOKE_OUT }}/sim.jsonl
${{ runner.temp }}/${{ env.SMOKE_OUT }}/node-logs/*.log
`
	backupQualificationCommand = `set -o pipefail
WK_E2E_BINARY="$RUNNER_TEMP/wukongim-backup-e2e" timeout --signal=TERM --kill-after=30s 10m \
  go test -tags=e2e ./test/e2e/backup/... -count=1 -timeout=8m -p=1 2>&1 | tee "$LOG_FILE"
`
	backupQualificationUnitCommand = `set -o pipefail
timeout --signal=TERM --kill-after=30s 12m \
  bash -c '
    go test ./scripts -run "^TestBackupQualification" -count=1 -timeout=2m &&
    go test ./pkg/backup ./internal/usecase/backup ./internal/infra/backup \
      ./internal/runtime/backup ./internal/access/manager ./internal/access/node \
      ./internal/app ./internal/config ./pkg/channel/store ./pkg/db/message ./pkg/db/meta \
      ./pkg/controller/state ./pkg/controller/fsm \
      -count=1 -timeout=10m -p=1
  ' 2>&1 | tee "$LOG_FILE"
`
	backupQualificationRaceCommand = `set -o pipefail
timeout --signal=TERM --kill-after=30s 12m \
  go test -race ./pkg/backup ./internal/usecase/backup ./internal/infra/backup \
    ./internal/runtime/backup ./pkg/channel/store ./pkg/db/message \
    -count=1 -timeout=10m -p=1 2>&1 | tee "$LOG_FILE"
`
	backupQualificationReceiptCommand = `{
  printf '%s\n' '## Backup qualification passed'
  printf '%s\n' "- Commit: $GITHUB_SHA"
  printf '%s\n' "- Run: $GITHUB_RUN_ID attempt $GITHUB_RUN_ATTEMPT"
  printf '%s\n' '- Backup artifact, signature, encryption, corruption, retention, restore, and Controller invariants passed.'
  printf '%s\n' '- Targeted race detection passed.'
  printf '%s\n' '- Three-node backup/restore and sustained failure scenarios passed.'
  printf '%s\n' ''
  printf '%s\n' 'This is repository qualification evidence, not a proof of external S3/KMS/Object Lock/IAM behavior or an absolute guarantee against every possible defect.'
} | tee "$RECEIPT_FILE" >> "$GITHUB_STEP_SUMMARY"
`
	backupQualificationEvidenceCommand = `: > "$EVIDENCE_FILE"
found=0
for log_file in "$UNIT_LOG_FILE" "$RACE_LOG_FILE" "$E2E_LOG_FILE"; do
  if [[ -f "$log_file" ]]; then
    found=1
    printf '===== %s =====\n' "$(basename "$log_file")" >> "$EVIDENCE_FILE"
    tail -c 340000 "$log_file" >> "$EVIDENCE_FILE"
    printf '\n' >> "$EVIDENCE_FILE"
  fi
done
if [[ "$found" -eq 0 ]]; then
  printf '%s\n' 'backup qualification log was not created' > "$EVIDENCE_FILE"
fi
`
)

var expectedNightlyJobs = map[string]ciJob{
	"go-race": {
		Name:           "Go race (${{ matrix.name }})",
		RunsOn:         "ubuntu-24.04",
		TimeoutMinutes: 45,
		Env: map[string]string{
			"GOWORK":      "off",
			"CGO_ENABLED": "1",
		},
		Strategy: &ciStrategy{
			FailFast: boolPointer(false),
			Matrix: ciMatrix{Include: []ciMatrixEntry{
				{Name: "internal-runtime", Packages: "./internal/app ./internal/runtime/..."},
				{Name: "plugin-runtime", Packages: "./pkg/plugin/pluginhost ./pkg/wklog"},
				{Name: "gateway-transport", Packages: "./pkg/gateway/... ./pkg/transport/..."},
				{Name: "channel-cluster-slot", Packages: "./pkg/channel/... ./pkg/cluster/... ./pkg/slot/..."},
			}},
		},
		Steps: []ciStep{
			checkoutStep(),
			setupGoStep(),
			verifyGoToolchainStep(),
			{
				Name:  "Run race group",
				Shell: "bash",
				Env: map[string]string{
					"PACKAGES": "${{ matrix.packages }}",
					"LOG_FILE": "${{ runner.temp }}/go-race-${{ matrix.name }}.log",
				},
				Run: nightlyRaceCommand,
			},
			{
				Name: "Upload race failure log",
				If:   "failure()",
				Uses: "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
				With: map[string]any{
					"name":              "go-race-${{ matrix.name }}-${{ github.run_id }}-${{ github.run_attempt }}",
					"path":              "${{ runner.temp }}/go-race-${{ matrix.name }}.log",
					"if-no-files-found": "warn",
					"retention-days":    7,
				},
			},
		},
	},
	"go-integration": {
		Name:           "Go integration",
		RunsOn:         "ubuntu-24.04",
		TimeoutMinutes: 30,
		Env:            map[string]string{"GOWORK": "off"},
		Steps: []ciStep{
			checkoutStep(),
			setupGoStep(),
			verifyGoToolchainStep(),
			{
				Name:  "Run integration packages",
				Shell: "bash",
				Env:   map[string]string{"LOG_FILE": "${{ runner.temp }}/go-integration.log"},
				Run:   nightlyIntegrationCommand,
			},
			{
				Name: "Upload integration failure log",
				If:   "failure()",
				Uses: "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
				With: map[string]any{
					"name":              "go-integration-${{ github.run_id }}-${{ github.run_attempt }}",
					"path":              "${{ runner.temp }}/go-integration.log",
					"if-no-files-found": "warn",
					"retention-days":    7,
				},
			},
		},
	},
	"go-e2e": {
		Name:           "Go e2e",
		RunsOn:         "ubuntu-24.04",
		TimeoutMinutes: 60,
		Env: map[string]string{
			"GOWORK": "off",
		},
		Steps: []ciStep{
			checkoutStep(),
			setupGoStep(),
			verifyGoToolchainStep(),
			{
				Name:  "Build e2e binary once",
				Shell: "bash",
				Run:   `go build -tags=e2e -o "$RUNNER_TEMP/wukongim-e2e" ./cmd/wukongim`,
			},
			{
				Name:  "Run e2e packages",
				Shell: "bash",
				Env:   map[string]string{"LOG_FILE": "${{ runner.temp }}/go-e2e.log"},
				Run:   nightlyE2ECommand,
			},
			{
				Name:  "Run bounded Cloud Medium recipient gate",
				Shell: "bash",
				Env: map[string]string{
					"LOG_FILE":                        "${{ runner.temp }}/go-e2e-medium-recipient.log",
					"WK_E2E_BINARY":                   "${{ runner.temp }}/wukongim-e2e",
					"WK_E2E_MEDIUM_RECIPIENT_HOTPATH": "1",
					"WK_E2E_MEDIUM_RECIPIENT_ENFORCE_ACCEPTANCE": "1",
					"WK_E2E_MEDIUM_RECIPIENT_QPS":                "500",
					"WK_E2E_MEDIUM_RECIPIENT_CI_SCALE":           "1",
				},
				Run: nightlyMediumRecipientCommand,
			},
			{
				Name: "Upload Cloud Medium recipient evidence",
				If:   "always()",
				Uses: "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
				With: map[string]any{
					"name":              "go-e2e-medium-recipient-${{ github.run_id }}-${{ github.run_attempt }}",
					"path":              "${{ runner.temp }}/go-e2e-medium-recipient.log",
					"if-no-files-found": "warn",
					"retention-days":    14,
				},
			},
			{
				Name: "Upload e2e failure log",
				If:   "failure()",
				Uses: "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
				With: map[string]any{
					"name":              "go-e2e-${{ github.run_id }}-${{ github.run_attempt }}",
					"path":              "${{ runner.temp }}/go-e2e.log",
					"if-no-files-found": "warn",
					"retention-days":    7,
				},
			},
		},
	},
	"three-node-smoke": {
		Name:           "Three-node smoke",
		RunsOn:         "ubuntu-24.04",
		TimeoutMinutes: 30,
		Env: map[string]string{
			"GOWORK":    "off",
			"SMOKE_OUT": "wkcli-sim-three-node-smoke",
			"WK_WUKONGIM_THREE_NODES_PROMETHEUS_ENABLE":              "false",
			"WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_NODE":                "false",
			"WK_WKCLI_SIM_THREE_SMOKE_AUTO_PROMOTE_CONTROLLER_VOTER": "false",
			"WK_WKCLI_SIM_THREE_SMOKE_FAULT_KILL_NODE":               "false",
		},
		Steps: []ciStep{
			checkoutStep(),
			setupGoStep(),
			verifyGoToolchainStep(),
			{
				Name:  "Run base three-node smoke",
				Shell: "bash",
				Run:   nightlySmokeCommand,
			},
			{
				Name: "Upload allowlisted smoke failure evidence",
				If:   "failure()",
				Uses: "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
				With: map[string]any{
					"name":              "three-node-smoke-${{ github.run_id }}-${{ github.run_attempt }}",
					"path":              nightlySmokeArtifactPaths,
					"if-no-files-found": "warn",
					"retention-days":    7,
				},
			},
		},
	},
}

var expectedBackupQualificationJobs = map[string]ciJob{
	"three-node-backup": {
		Name:           "Backup release gate",
		RunsOn:         "ubuntu-24.04",
		TimeoutMinutes: 40,
		Env: map[string]string{
			"GOWORK":      "off",
			"CGO_ENABLED": "1",
		},
		Steps: []ciStep{
			checkoutStep(),
			setupGoStep(),
			verifyGoToolchainStep(),
			{
				Name:  "Verify backup and restore contracts",
				Shell: "bash",
				Env:   map[string]string{"LOG_FILE": "${{ runner.temp }}/backup-contracts.log"},
				Run:   backupQualificationUnitCommand,
			},
			{
				Name:  "Detect backup data races",
				Shell: "bash",
				Env:   map[string]string{"LOG_FILE": "${{ runner.temp }}/backup-race.log"},
				Run:   backupQualificationRaceCommand,
			},
			{
				Name:  "Build e2e-tagged product binary",
				Shell: "bash",
				Run:   `go build -tags=e2e -o "$RUNNER_TEMP/wukongim-backup-e2e" ./cmd/wukongim`,
			},
			{
				Name:  "Run backup qualification scenarios",
				Shell: "bash",
				Env:   map[string]string{"LOG_FILE": "${{ runner.temp }}/backup-qualification.log"},
				Run:   backupQualificationCommand,
			},
			{
				Name:  "Write qualification receipt",
				If:    "success()",
				Shell: "bash",
				Env:   map[string]string{"RECEIPT_FILE": "${{ runner.temp }}/backup-qualification-receipt.md"},
				Run:   backupQualificationReceiptCommand,
			},
			{
				Name: "Upload qualification receipt",
				If:   "success()",
				Uses: "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
				With: map[string]any{
					"name":              "backup-qualification-receipt-${{ github.run_id }}-${{ github.run_attempt }}",
					"path":              "${{ runner.temp }}/backup-qualification-receipt.md",
					"if-no-files-found": "error",
					"retention-days":    30,
				},
			},
			{
				Name:  "Prepare bounded failure evidence",
				If:    "failure()",
				Shell: "bash",
				Env: map[string]string{
					"UNIT_LOG_FILE": "${{ runner.temp }}/backup-contracts.log",
					"RACE_LOG_FILE": "${{ runner.temp }}/backup-race.log",
					"E2E_LOG_FILE":  "${{ runner.temp }}/backup-qualification.log",
					"EVIDENCE_FILE": "${{ runner.temp }}/backup-qualification-failure.log",
				},
				Run: backupQualificationEvidenceCommand,
			},
			{
				Name: "Upload bounded failure evidence",
				If:   "failure()",
				Uses: "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
				With: map[string]any{
					"name":              "backup-qualification-${{ github.run_id }}-${{ github.run_attempt }}",
					"path":              "${{ runner.temp }}/backup-qualification-failure.log",
					"if-no-files-found": "warn",
					"retention-days":    7,
				},
			},
		},
	},
}

func checkoutStep() ciStep {
	return ciStep{
		Uses: "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0",
		With: map[string]any{"persist-credentials": false},
	}
}

func setupGoStep() ciStep {
	return ciStep{
		Uses: "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16",
		With: map[string]any{
			"go-version-file":       "go.mod",
			"cache":                 true,
			"cache-dependency-path": "go.sum",
		},
	}
}

func verifyGoToolchainStep() ciStep {
	return ciStep{Name: "Verify Go toolchain", Run: `test "$(go env GOVERSION)" = "go1.25.11"`}
}

func TestCIWorkflowContract(t *testing.T) {
	raw := readWorkflow(t, "ci.yml")
	if err := validateCIWorkflow(raw); err != nil {
		t.Fatal(err)
	}
}

func TestNightlyWorkflowContract(t *testing.T) {
	raw := readWorkflow(t, "nightly.yml")
	if err := validateNightlyWorkflow(raw); err != nil {
		t.Fatal(err)
	}
}

func TestBackupQualificationWorkflowContract(t *testing.T) {
	raw := readWorkflow(t, "backup-qualification.yml")
	if err := validateBackupQualificationWorkflow(raw); err != nil {
		t.Fatal(err)
	}
}

func TestBackupQualificationWorkflowRejectsUntaggedBinary(t *testing.T) {
	raw := string(readWorkflow(t, "backup-qualification.yml"))
	mutated := replaceWorkflowFirst(t, raw,
		`        run: go build -tags=e2e -o "$RUNNER_TEMP/wukongim-backup-e2e" ./cmd/wukongim`,
		`        run: go build -o "$RUNNER_TEMP/wukongim-backup-e2e" ./cmd/wukongim`,
	)
	if err := validateBackupQualificationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted an untagged backup qualification binary")
	}
}

func TestBackupQualificationWorkflowRejectsAutomaticTrigger(t *testing.T) {
	raw := string(readWorkflow(t, "backup-qualification.yml"))
	mutated := replaceWorkflowFirst(t, raw, "  workflow_dispatch:\n", "  pull_request:\n  workflow_dispatch:\n")
	if err := validateBackupQualificationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted an automatic backup qualification trigger")
	}
}

func TestBackupQualificationWorkflowRejectsDroppedRaceGate(t *testing.T) {
	raw := string(readWorkflow(t, "backup-qualification.yml"))
	mutated := replaceWorkflowFirst(t, raw,
		"      - name: Detect backup data races\n",
		"      - name: Skip backup data races\n",
	)
	if err := validateBackupQualificationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted a backup workflow without the required race gate")
	}
}

func TestCIWorkflowContractRejectsMutations(t *testing.T) {
	raw := string(readWorkflow(t, "ci.yml"))
	tests := []struct {
		name      string
		mutate    func(*testing.T, string) string
		wantError string
	}{
		{
			name: "named action step using floating ref",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0",
					"      - name: Unreviewed action\n        uses: owner/action@main",
				)
			},
			wantError: "unreviewed action",
		},
		{
			name: "job level reusable workflow using floating ref",
			mutate: func(_ *testing.T, workflow string) string {
				return workflow + "\n  reusable:\n    uses: owner/workflow@main\n"
			},
			wantError: "unreviewed action",
		},
		{
			name: "commented triggers do not replace on hierarchy",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"on:\n  pull_request:\n  push:\n    branches: [main]\n  workflow_dispatch:",
					"on: {}\n# pull_request:\n# push:\n#   branches: [main]\n# workflow_dispatch:",
				)
			},
		},
		{
			name: "commented read permission does not mask write all",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"permissions:\n  contents: read",
					"permissions: write-all\n# permissions:\n#   contents: read",
				)
			},
		},
		{
			name: "commented timeout does not mask wrong job timeout",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"    timeout-minutes: 10",
					"    timeout-minutes: 11 # timeout-minutes: 10",
				)
			},
		},
		{
			name: "ci concurrency must be an explicit boolean",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"  cancel-in-progress: true",
					"  cancel-in-progress: null # cancel-in-progress: true",
				)
			},
		},
		{
			name: "unit command cannot add benchmark",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"        run: go test $PACKAGES -count=1 -timeout=14m",
					"        run: go test $PACKAGES -bench=. -count=1 -timeout=14m # go test $PACKAGES -count=1 -timeout=14m",
				)
			},
		},
		{
			name: "unit command cannot add race after package operand",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"        run: go test $PACKAGES -count=1 -timeout=14m",
					"        run: go test $PACKAGES -race -count=1 -timeout=14m # go test $PACKAGES -count=1 -timeout=14m",
				)
			},
		},
		{
			name: "job cannot declare null continue on error",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"    timeout-minutes: 10\n    env:\n      GOWORK: \"off\"",
					"    timeout-minutes: 10\n    continue-on-error: null\n    env:\n      GOWORK: \"off\"",
				)
			},
		},
		{
			name: "step cannot declare null continue on error",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"      - name: Verify Go toolchain\n        run: test \"$(go env GOVERSION)\" = \"go1.25.11\"",
					"      - name: Verify Go toolchain\n        continue-on-error: null\n        run: test \"$(go env GOVERSION)\" = \"go1.25.11\"",
				)
			},
		},
		{
			name: "web job cannot declare null strategy",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"  web:\n    name: Web\n    runs-on: ubuntu-24.04\n    timeout-minutes: 10\n    defaults:",
					"  web:\n    name: Web\n    runs-on: ubuntu-24.04\n    timeout-minutes: 10\n    strategy: null\n    defaults:",
				)
			},
		},
		{
			name: "run step cannot declare null with",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"      - name: Verify Bun version\n        run: test \"$(bun --version)\" = \"1.3.11\"",
					"      - name: Verify Bun version\n        with: null\n        run: test \"$(bun --version)\" = \"1.3.11\"",
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mutated := []byte(tt.mutate(t, raw))
			err := validateCIWorkflow(mutated)
			if err == nil {
				t.Fatal("validator accepted mutated workflow")
			}
			if tt.wantError != "" && !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("validation error %q does not contain %q", err, tt.wantError)
			}
		})
	}
}

func TestNightlyWorkflowContractRejectsMutations(t *testing.T) {
	raw := string(readWorkflow(t, "nightly.yml"))
	tests := []struct {
		name      string
		mutate    func(*testing.T, string) string
		wantError string
	}{
		{
			name: "schedule cannot be replaced by push",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"  schedule:\n    - cron: \"0 18 * * *\"",
					"  push:\n    branches: [main]\n  # schedule:\n  #   - cron: \"0 18 * * *\"",
				)
			},
		},
		{
			name: "cron must remain exact",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					`    - cron: "0 18 * * *"`,
					`    - cron: "0 19 * * *" # cron: "0 18 * * *"`,
				)
			},
		},
		{
			name: "manual trigger cannot gain inputs",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"  workflow_dispatch:",
					"  workflow_dispatch:\n    inputs:\n      unsafe:\n        required: false",
				)
			},
		},
		{
			name: "nightly concurrency must be an explicit boolean",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"  cancel-in-progress: false",
					"  cancel-in-progress: null # cancel-in-progress: false",
				)
			},
		},
		{
			name: "job identifiers are exact",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow, "  go-race:", "  race: # go-race:")
			},
		},
		{
			name: "race matrix package groups are exact",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					`            packages: "./internal/app ./internal/runtime/..."`,
					`            packages: "./internal/app ./internal/..." # packages: "./internal/app ./internal/runtime/..."`,
				)
			},
		},
		{
			name: "race command is exact",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					`          timeout --signal=TERM --kill-after=30s 40m go test -race $PACKAGES -count=1 -timeout=35m -p=1 2>&1 | tee "$LOG_FILE"`,
					`          timeout --signal=TERM --kill-after=30s 40m go test -race $PACKAGES -shuffle=on -count=1 -timeout=35m -p=1 2>&1 | tee "$LOG_FILE" # timeout --signal=TERM --kill-after=30s 40m go test -race $PACKAGES -count=1 -timeout=35m -p=1`,
				)
			},
		},
		{
			name: "integration command is exact",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					`          timeout --signal=TERM --kill-after=30s 25m go test -tags=integration ./internal/... ./pkg/... -count=1 -timeout=20m -p=1 2>&1 | tee "$LOG_FILE"`,
					`          timeout --signal=TERM --kill-after=30s 25m go test -tags=integration ./internal/... ./pkg/... -shuffle=on -count=1 -timeout=20m -p=1 2>&1 | tee "$LOG_FILE" # timeout --signal=TERM --kill-after=30s 25m go test -tags=integration ./internal/... ./pkg/... -count=1 -timeout=20m -p=1`,
				)
			},
		},
		{
			name: "e2e prebuild command is exact",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					`        run: go build -tags=e2e -o "$RUNNER_TEMP/wukongim-e2e" ./cmd/wukongim`,
					`        run: go build -o "$RUNNER_TEMP/wukongim-e2e" ./cmd/wukongim # go build -tags=e2e -o "$RUNNER_TEMP/wukongim-e2e" ./cmd/wukongim`,
				)
			},
		},
		{
			name: "e2e command is exact",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					`          WK_E2E_BINARY="$RUNNER_TEMP/wukongim-e2e" timeout --signal=TERM --kill-after=30s 50m go test -tags=e2e ./test/e2e/... -count=1 -timeout=45m -p=1 2>&1 | tee "$LOG_FILE"`,
					`          WK_E2E_BINARY="$RUNNER_TEMP/wukongim-e2e" timeout --signal=TERM --kill-after=30s 50m go test -tags=e2e ./test/e2e/... -shuffle=on -count=1 -timeout=45m -p=1 2>&1 | tee "$LOG_FILE" # WK_E2E_BINARY="$RUNNER_TEMP/wukongim-e2e" timeout --signal=TERM --kill-after=30s 50m go test -tags=e2e ./test/e2e/... -count=1 -timeout=45m -p=1`,
				)
			},
		},
		{
			name: "smoke script command is exact",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"          timeout --signal=TERM --kill-after=30s 25m bash scripts/smoke-wkcli-sim-wukongim-three-nodes.sh \\",
					"          timeout --signal=TERM --kill-after=30s 25m bash scripts/smoke-wkcli-sim-wukongimv2-three-nodes.sh \\ # scripts/smoke-wkcli-sim-wukongim-three-nodes.sh",
				)
			},
		},
		{
			name: "smoke ready timeout is exact",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"            --ready-timeout 180",
					"            --ready-timeout 181 # --ready-timeout 180",
				)
			},
		},
		{
			name: "race job timeout is exact",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"  go-race:\n    name: Go race (${{ matrix.name }})\n    runs-on: ubuntu-24.04\n    timeout-minutes: 45",
					"  go-race:\n    name: Go race (${{ matrix.name }})\n    runs-on: ubuntu-24.04\n    timeout-minutes: 46 # timeout-minutes: 45",
				)
			},
		},
		{
			name: "integration job timeout is exact",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"  go-integration:\n    name: Go integration\n    runs-on: ubuntu-24.04\n    timeout-minutes: 30",
					"  go-integration:\n    name: Go integration\n    runs-on: ubuntu-24.04\n    timeout-minutes: 31 # timeout-minutes: 30",
				)
			},
		},
		{
			name: "e2e job timeout is exact",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"  go-e2e:\n    name: Go e2e\n    runs-on: ubuntu-24.04\n    timeout-minutes: 60",
					"  go-e2e:\n    name: Go e2e\n    runs-on: ubuntu-24.04\n    timeout-minutes: 61 # timeout-minutes: 60",
				)
			},
		},
		{
			name: "smoke job timeout is exact",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"  three-node-smoke:\n    name: Three-node smoke\n    runs-on: ubuntu-24.04\n    timeout-minutes: 30",
					"  three-node-smoke:\n    name: Three-node smoke\n    runs-on: ubuntu-24.04\n    timeout-minutes: 31 # timeout-minutes: 30",
				)
			},
		},
		{
			name: "action refs must remain pinned",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0",
					"      - uses: actions/checkout@main # v7.0.0 actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0",
				)
			},
			wantError: "ref =",
		},
		{
			name: "failure artifact cannot upload on success",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"        if: failure()",
					"        if: always() # if: failure()",
				)
			},
		},
		{
			name: "failure artifact retention is exact",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"          retention-days: 7",
					"          retention-days: 8 # retention-days: 7",
				)
			},
		},
		{
			name: "smoke artifact cannot upload whole output directory",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"            ${{ runner.temp }}/${{ env.SMOKE_OUT }}/summary.md",
					"            ${{ runner.temp }}/${{ env.SMOKE_OUT }} # ${{ runner.temp }}/${{ env.SMOKE_OUT }}/summary.md",
				)
			},
		},
		{
			name: "smoke artifact cannot upload manager token",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"            ${{ runner.temp }}/${{ env.SMOKE_OUT }}/sim.jsonl",
					"            ${{ runner.temp }}/${{ env.SMOKE_OUT }}/manager-token # ${{ runner.temp }}/${{ env.SMOKE_OUT }}/sim.jsonl",
				)
			},
		},
		{
			name: "nightly cannot enable opt in e2e stress",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"  go-e2e:\n    name: Go e2e\n    runs-on: ubuntu-24.04\n    timeout-minutes: 60\n    env:\n      GOWORK: \"off\"",
					"  go-e2e:\n    name: Go e2e\n    runs-on: ubuntu-24.04\n    timeout-minutes: 60\n    env:\n      GOWORK: \"off\"\n      WK_E2E_100K_CONVERSATION: \"1\"",
				)
			},
		},
		{
			name: "base smoke flags remain disabled",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					`      WK_WKCLI_SIM_THREE_SMOKE_FAULT_KILL_NODE: "false"`,
					`      WK_WKCLI_SIM_THREE_SMOKE_FAULT_KILL_NODE: "true" # "false"`,
				)
			},
		},
		{
			name: "steps cannot continue on error",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"      - name: Run race group\n        shell: bash\n        env:",
					"      - name: Run race group\n        shell: bash\n        continue-on-error: true\n        env:",
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mutated := []byte(tt.mutate(t, raw))
			err := validateNightlyWorkflow(mutated)
			if err == nil {
				t.Fatal("validator accepted mutated workflow")
			}
			if tt.wantError != "" && !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("validation error %q does not contain %q", err, tt.wantError)
			}
		})
	}
}

func replaceWorkflowFirst(t *testing.T, workflow, old, replacement string) string {
	t.Helper()
	if !strings.Contains(workflow, old) {
		t.Fatalf("workflow mutation source is missing: %q", old)
	}
	return strings.Replace(workflow, old, replacement, 1)
}

func validateCIWorkflow(raw []byte) error {
	document, workflow, err := decodeWorkflow(raw)
	if err != nil {
		return err
	}
	if err := validateAllUses(document, raw); err != nil {
		return err
	}
	if err := validateWorkflowStructure(
		document,
		[]string{"go-quality", "go-unit", "web", "demo"},
		expectedCIJobs,
	); err != nil {
		return err
	}
	if workflow.Name != "CI" {
		return fmt.Errorf("workflow name = %q, want CI", workflow.Name)
	}
	if err := validateCITriggers(workflow.On); err != nil {
		return err
	}
	wantPermissions := map[string]string{"contents": "read"}
	if !reflect.DeepEqual(workflow.Permissions, wantPermissions) {
		return fmt.Errorf("permissions = %#v, want exactly %#v", workflow.Permissions, wantPermissions)
	}
	wantConcurrency := ciConcurrency{
		Group:            "${{ github.workflow }}-${{ github.event.pull_request.number || github.ref }}",
		CancelInProgress: boolPointer(true),
	}
	if !reflect.DeepEqual(workflow.Concurrency, wantConcurrency) {
		return fmt.Errorf("concurrency = %#v, want %#v", workflow.Concurrency, wantConcurrency)
	}
	if len(workflow.Jobs) != len(expectedCIJobs) {
		return fmt.Errorf("workflow jobs = %d, want exactly %d", len(workflow.Jobs), len(expectedCIJobs))
	}
	for name, want := range expectedCIJobs {
		got, ok := workflow.Jobs[name]
		if !ok {
			return fmt.Errorf("workflow missing required job %q", name)
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("job %q does not match the required fail-closed contract", name)
		}
	}
	return nil
}

func validateNightlyWorkflow(raw []byte) error {
	return validateScheduledWorkflow(
		raw,
		"Nightly",
		"0 18 * * *",
		[]string{"go-race", "go-integration", "go-e2e", "three-node-smoke"},
		expectedNightlyJobs,
	)
}

func validateBackupQualificationWorkflow(raw []byte) error {
	document, workflow, err := decodeWorkflow(raw)
	if err != nil {
		return err
	}
	if err := validateAllUses(document, raw); err != nil {
		return err
	}
	if err := validateWorkflowStructure(
		document,
		[]string{"three-node-backup"},
		expectedBackupQualificationJobs,
	); err != nil {
		return err
	}
	if workflow.Name != "Backup qualification" {
		return fmt.Errorf("workflow name = %q, want Backup qualification", workflow.Name)
	}
	if err := validateBackupQualificationTriggers(workflow.On); err != nil {
		return err
	}
	wantPermissions := map[string]string{"contents": "read"}
	if !reflect.DeepEqual(workflow.Permissions, wantPermissions) {
		return fmt.Errorf("permissions = %#v, want exactly %#v", workflow.Permissions, wantPermissions)
	}
	wantConcurrency := ciConcurrency{
		Group:            "${{ github.workflow }}-${{ github.ref }}",
		CancelInProgress: boolPointer(true),
	}
	if !reflect.DeepEqual(workflow.Concurrency, wantConcurrency) {
		return fmt.Errorf("concurrency = %#v, want %#v", workflow.Concurrency, wantConcurrency)
	}
	if len(workflow.Jobs) != len(expectedBackupQualificationJobs) {
		return fmt.Errorf("workflow jobs = %d, want exactly %d", len(workflow.Jobs), len(expectedBackupQualificationJobs))
	}
	for name, want := range expectedBackupQualificationJobs {
		got, ok := workflow.Jobs[name]
		if !ok {
			return fmt.Errorf("workflow missing required job %q", name)
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("job %q does not match the required fail-closed contract", name)
		}
	}
	return nil
}

func validateScheduledWorkflow(
	raw []byte,
	wantName string,
	wantCron string,
	jobNames []string,
	expectedJobs map[string]ciJob,
) error {
	document, workflow, err := decodeWorkflow(raw)
	if err != nil {
		return err
	}
	if err := validateAllUses(document, raw); err != nil {
		return err
	}
	if err := validateWorkflowStructure(
		document,
		jobNames,
		expectedJobs,
	); err != nil {
		return err
	}
	if workflow.Name != wantName {
		return fmt.Errorf("workflow name = %q, want %s", workflow.Name, wantName)
	}
	if err := validateScheduledTriggers(workflow.On, wantName, wantCron); err != nil {
		return err
	}
	wantPermissions := map[string]string{"contents": "read"}
	if !reflect.DeepEqual(workflow.Permissions, wantPermissions) {
		return fmt.Errorf("permissions = %#v, want exactly %#v", workflow.Permissions, wantPermissions)
	}
	wantConcurrency := ciConcurrency{
		Group:            "${{ github.workflow }}-${{ github.ref }}",
		CancelInProgress: boolPointer(false),
	}
	if !reflect.DeepEqual(workflow.Concurrency, wantConcurrency) {
		return fmt.Errorf("concurrency = %#v, want %#v", workflow.Concurrency, wantConcurrency)
	}
	if len(workflow.Jobs) != len(expectedJobs) {
		return fmt.Errorf("workflow jobs = %d, want exactly %d", len(workflow.Jobs), len(expectedJobs))
	}
	for name, want := range expectedJobs {
		got, ok := workflow.Jobs[name]
		if !ok {
			return fmt.Errorf("workflow missing required job %q", name)
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("job %q does not match the required fail-closed contract", name)
		}
	}
	return nil
}

func validateWorkflowStructure(document *yaml.Node, jobNames []string, expectedJobs map[string]ciJob) error {
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return fmt.Errorf("workflow YAML must contain one mapping document")
	}
	root := document.Content[0]
	if err := validateMappingKeys(
		root,
		[]string{"name", "on", "permissions", "concurrency", "jobs"},
		"workflow root",
	); err != nil {
		return err
	}
	permissions, ok := mappingValue(root, "permissions")
	if !ok {
		return fmt.Errorf("workflow permissions are missing")
	}
	if err := validateMappingKeys(permissions, []string{"contents"}, "workflow permissions"); err != nil {
		return err
	}
	concurrency, ok := mappingValue(root, "concurrency")
	if !ok {
		return fmt.Errorf("workflow concurrency is missing")
	}
	if err := validateMappingKeys(
		concurrency,
		[]string{"group", "cancel-in-progress"},
		"workflow concurrency",
	); err != nil {
		return err
	}
	jobs, ok := mappingValue(root, "jobs")
	if !ok || jobs.Kind != yaml.MappingNode {
		return fmt.Errorf("workflow jobs must be a mapping")
	}
	if err := validateMappingKeys(jobs, jobNames, "workflow jobs"); err != nil {
		return err
	}
	for _, name := range jobNames {
		wantJob := expectedJobs[name]
		job, ok := mappingValue(jobs, name)
		if !ok {
			return fmt.Errorf("workflow missing required job %q", name)
		}
		if err := validateMappingKeys(job, expectedJobKeys(wantJob), fmt.Sprintf("job %q", name)); err != nil {
			return err
		}
		steps, ok := mappingValue(job, "steps")
		if !ok || steps.Kind != yaml.SequenceNode {
			return fmt.Errorf("job %q steps must be a sequence", name)
		}
		if len(steps.Content) != len(wantJob.Steps) {
			return fmt.Errorf("job %q steps = %d, want exactly %d", name, len(steps.Content), len(wantJob.Steps))
		}
		for index, wantStep := range wantJob.Steps {
			context := fmt.Sprintf("job %q step %d", name, index+1)
			if err := validateMappingKeys(steps.Content[index], expectedStepKeys(wantStep), context); err != nil {
				return err
			}
		}
	}
	return nil
}

func expectedJobKeys(job ciJob) []string {
	keys := []string{"name", "runs-on", "timeout-minutes", "steps"}
	if job.Env != nil {
		keys = append(keys, "env")
	}
	if job.Defaults != nil {
		keys = append(keys, "defaults")
	}
	if job.Strategy != nil {
		keys = append(keys, "strategy")
	}
	if job.Uses != "" {
		keys = append(keys, "uses")
	}
	return keys
}

func expectedStepKeys(step ciStep) []string {
	var keys []string
	if step.Name != "" {
		keys = append(keys, "name")
	}
	if step.Uses != "" {
		keys = append(keys, "uses")
	}
	if step.Run != "" {
		keys = append(keys, "run")
	}
	if step.Shell != "" {
		keys = append(keys, "shell")
	}
	if step.If != "" {
		keys = append(keys, "if")
	}
	if step.Env != nil {
		keys = append(keys, "env")
	}
	if step.With != nil {
		keys = append(keys, "with")
	}
	return keys
}

func validateMappingKeys(node *yaml.Node, expected []string, context string) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s must be a mapping", context)
	}
	if len(node.Content) != len(expected)*2 {
		return fmt.Errorf("%s has %d keys, want exactly %d", context, len(node.Content)/2, len(expected))
	}
	actual := make(map[string]struct{}, len(expected))
	for index := 0; index+1 < len(node.Content); index += 2 {
		key := node.Content[index]
		if key.Kind != yaml.ScalarNode {
			return fmt.Errorf("%s contains a non-scalar key", context)
		}
		if _, duplicate := actual[key.Value]; duplicate {
			return fmt.Errorf("%s contains duplicate key %q", context, key.Value)
		}
		actual[key.Value] = struct{}{}
	}
	for _, key := range expected {
		if _, ok := actual[key]; !ok {
			return fmt.Errorf("%s is missing required key %q", context, key)
		}
	}
	return nil
}

func mappingValue(mapping *yaml.Node, name string) (*yaml.Node, bool) {
	if mapping.Kind != yaml.MappingNode {
		return nil, false
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		key := mapping.Content[index]
		if key.Kind == yaml.ScalarNode && key.Value == name {
			return mapping.Content[index+1], true
		}
	}
	return nil, false
}

func decodeWorkflow(raw []byte) (*yaml.Node, ciWorkflow, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		if err == io.EOF {
			return nil, ciWorkflow{}, fmt.Errorf("workflow YAML is empty")
		}
		return nil, ciWorkflow{}, fmt.Errorf("parse workflow YAML: %w", err)
	}
	if len(document.Content) == 0 {
		return nil, ciWorkflow{}, fmt.Errorf("workflow YAML is empty")
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, ciWorkflow{}, fmt.Errorf("workflow YAML must contain exactly one document")
		}
		return nil, ciWorkflow{}, fmt.Errorf("parse trailing workflow YAML: %w", err)
	}

	typedDecoder := yaml.NewDecoder(bytes.NewReader(raw))
	typedDecoder.KnownFields(true)
	var workflow ciWorkflow
	if err := typedDecoder.Decode(&workflow); err != nil {
		return nil, ciWorkflow{}, fmt.Errorf("decode workflow hierarchy: %w", err)
	}
	return &document, workflow, nil
}

func validateCITriggers(triggers map[string]yaml.Node) error {
	for _, name := range []string{"pull_request", "push", "workflow_dispatch"} {
		if _, ok := triggers[name]; !ok {
			return fmt.Errorf("workflow trigger %q is missing", name)
		}
	}
	if len(triggers) != 3 {
		return fmt.Errorf("workflow trigger keys = %d, want exactly pull_request, push, and workflow_dispatch", len(triggers))
	}
	for _, name := range []string{"pull_request", "workflow_dispatch"} {
		trigger := triggers[name]
		if !isEmptyTrigger(trigger) {
			return fmt.Errorf("workflow trigger %q must not contain filters or options", name)
		}
	}

	push := triggers["push"]
	if push.Kind != yaml.MappingNode || len(push.Content) != 2 {
		return fmt.Errorf("push trigger must contain only branches: [main]")
	}
	if key := push.Content[0]; key.Kind != yaml.ScalarNode || key.Value != "branches" {
		return fmt.Errorf("push trigger must contain only branches: [main]")
	}
	branches := push.Content[1]
	if branches.Kind != yaml.SequenceNode || len(branches.Content) != 1 || branches.Content[0].Value != "main" {
		return fmt.Errorf("push branches must be exactly [main]")
	}
	return nil
}

func validateScheduledTriggers(triggers map[string]yaml.Node, workflowName, wantCron string) error {
	if len(triggers) != 2 {
		return fmt.Errorf("workflow trigger keys = %d, want exactly schedule and workflow_dispatch", len(triggers))
	}
	schedule, ok := triggers["schedule"]
	if !ok {
		return fmt.Errorf("workflow trigger %q is missing", "schedule")
	}
	workflowDispatch, ok := triggers["workflow_dispatch"]
	if !ok {
		return fmt.Errorf("workflow trigger %q is missing", "workflow_dispatch")
	}
	if !isEmptyTrigger(workflowDispatch) {
		return fmt.Errorf("workflow trigger %q must not contain inputs or options", "workflow_dispatch")
	}
	if schedule.Kind != yaml.SequenceNode || len(schedule.Content) != 1 {
		return fmt.Errorf("schedule trigger must contain exactly one cron entry")
	}
	entry := schedule.Content[0]
	context := strings.ToLower(workflowName) + " schedule entry"
	if err := validateMappingKeys(entry, []string{"cron"}, context); err != nil {
		return err
	}
	cron, ok := mappingValue(entry, "cron")
	if !ok || cron.Kind != yaml.ScalarNode || cron.Value != wantCron {
		return fmt.Errorf("%s cron = %q, want exactly %q", strings.ToLower(workflowName), cronValue(cron), wantCron)
	}
	return nil
}

func validateBackupQualificationTriggers(triggers map[string]yaml.Node) error {
	if len(triggers) != 1 {
		return fmt.Errorf("backup qualification trigger keys = %d, want exactly workflow_dispatch", len(triggers))
	}
	trigger, ok := triggers["workflow_dispatch"]
	if !ok {
		return fmt.Errorf("workflow trigger %q is missing", "workflow_dispatch")
	}
	if !isEmptyTrigger(trigger) {
		return fmt.Errorf("workflow trigger %q must not contain inputs or options", "workflow_dispatch")
	}
	return nil
}

func cronValue(node *yaml.Node) string {
	if node == nil {
		return ""
	}
	return node.Value
}

func isEmptyTrigger(trigger yaml.Node) bool {
	return trigger.Kind == 0 || trigger.Tag == "!!null" ||
		(trigger.Kind == yaml.MappingNode && len(trigger.Content) == 0)
}

func validateAllUses(document *yaml.Node, raw []byte) error {
	uses := collectUses(document)
	if len(uses) == 0 {
		return fmt.Errorf("workflow contains no action references")
	}
	lines := strings.Split(string(raw), "\n")
	for _, node := range uses {
		if node.Kind != yaml.ScalarNode || node.Value == "" {
			return fmt.Errorf("action reference must be a non-empty scalar")
		}
		action, ref, ok := strings.Cut(node.Value, "@")
		if !ok || action == "" || ref == "" {
			return fmt.Errorf("action reference %q lacks a complete owner/action@ref", node.Value)
		}
		pin, approved := approvedActionPins[action]
		if !approved {
			return fmt.Errorf("unreviewed action %q", action)
		}
		if ref != pin.sha {
			return fmt.Errorf("action %s ref = %q, want immutable %s", action, ref, pin.sha)
		}
		if node.Line < 1 || node.Line > len(lines) || !strings.Contains(lines[node.Line-1], "# "+pin.release) {
			return fmt.Errorf("action %s must retain release comment %s on its uses line", action, pin.release)
		}
	}
	return nil
}

func collectUses(document *yaml.Node) []*yaml.Node {
	var uses []*yaml.Node
	seen := make(map[*yaml.Node]bool)
	var walk func(*yaml.Node)
	walk = func(node *yaml.Node) {
		if node == nil || seen[node] {
			return
		}
		seen[node] = true
		switch node.Kind {
		case yaml.MappingNode:
			for index := 0; index+1 < len(node.Content); index += 2 {
				key, value := node.Content[index], node.Content[index+1]
				if key.Kind == yaml.ScalarNode && key.Value == "uses" {
					uses = append(uses, value)
				}
				walk(value)
			}
		case yaml.AliasNode:
			walk(node.Alias)
		default:
			for _, child := range node.Content {
				walk(child)
			}
		}
	}
	walk(document)
	return uses
}

func boolPointer(value bool) *bool {
	return &value
}

func readWorkflow(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", ".github", "workflows", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read workflow %s: %v", path, err)
	}
	return raw
}
