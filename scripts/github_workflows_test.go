package scripts_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	RunName     string               `yaml:"run-name"`
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
	If             string            `yaml:"if"`
	RunsOn         string            `yaml:"runs-on"`
	TimeoutMinutes int               `yaml:"timeout-minutes"`
	Environment    string            `yaml:"environment"`
	Needs          []string          `yaml:"needs"`
	Permissions    map[string]string `yaml:"permissions"`
	Outputs        map[string]string `yaml:"outputs"`
	Concurrency    *ciConcurrency    `yaml:"concurrency"`
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
	ID    string            `yaml:"id"`
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

const (
	backupPortableFaultsCommand = `set -o pipefail
timeout --signal=TERM --kill-after=30s 15m \
  go test ./pkg/backup ./internal/infra/backup ./internal/runtime/backup ./internal/usecase/backup ./internal/app \
    -count=1 -timeout=12m 2>&1 | tee "$LOG_FILE"
`
	backupQualificationEvidenceCommand = `if [[ -f "$LOG_FILE" ]]; then
  tail -c 1048576 "$LOG_FILE" > "$EVIDENCE_FILE"
else
  printf '%s\n' 'backup qualification log was not created' > "$EVIDENCE_FILE"
fi
`
	backupPortableEvidenceCommand = `if [[ -f "$LOG_FILE" ]]; then
  tail -c 1048576 "$LOG_FILE" > "$EVIDENCE_FILE"
else
  printf '%s\n' 'backup portable fault log was not created' > "$EVIDENCE_FILE"
fi
`
	backupQualificationCommand = `set -o pipefail
WK_E2E_BINARY="$RUNNER_TEMP/wukongim-backup-e2e" timeout --signal=TERM --kill-after=30s 20m \
  go test -tags=e2e ./test/e2e/backup/... -count=1 -timeout=18m -p=1 2>&1 | tee "$LOG_FILE"
`
	backupScaleCommand = `set -euo pipefail
WK_E2E_BINARY="$RUNNER_TEMP/wukongim-backup-scale-e2e" timeout --signal=TERM --kill-after=30s 25m \
  go test -tags=e2e ./test/e2e/message/medium_recipient_hotpath \
    -run TestCloudMediumScaledRecipientHotPath -count=1 -timeout=23m -p=1 -v 2>&1 | tee "$LOG_FILE"
grep -q 'WK-BACKUP-SCALE-EVIDENCE ' "$LOG_FILE"
`
	backupScaleEvidenceCommand = `if [[ -f "$LOG_FILE" ]]; then
  tail -c 1048576 "$LOG_FILE" > "$EVIDENCE_FILE"
else
  printf '%s\n' 'backup scale performance log was not created' > "$EVIDENCE_FILE"
fi
`
	backupKeyCredentialCommand = `set -euo pipefail
umask 077
test -n "$BACKUP_KEY_PACKAGE_B64"
credential_directory="$RUNNER_TEMP/backup-credentials"
mkdir "$credential_directory"
printf '%s' "$BACKUP_KEY_PACKAGE_B64" | base64 --decode \
  > "$credential_directory/wukongim-backup-key-package"
chmod 0600 "$credential_directory/wukongim-backup-key-package"
printf 'CREDENTIALS_DIRECTORY=%s\n' "$credential_directory" \
  >> "$GITHUB_ENV"
`
	backupProductionCommand = `set -euo pipefail
required=(
  ALIBABA_CLOUD_ACCESS_KEY_ID ALIBABA_CLOUD_ACCESS_KEY_SECRET
  WK_E2E_BACKUP_REPOSITORY_ID WK_E2E_BACKUP_OBJECT_LOCK_DAYS
  CREDENTIALS_DIRECTORY
  WK_E2E_BACKUP_PRIMARY_ENDPOINT WK_E2E_BACKUP_PRIMARY_REGION
  WK_E2E_BACKUP_PRIMARY_BUCKET WK_E2E_BACKUP_PRIMARY_PREFIX
  WK_E2E_BACKUP_PRIMARY_ACCESS_ROLE_ARN
  WK_E2E_BACKUP_PRIMARY_REPAIR_ROLE_ARN WK_E2E_BACKUP_PRIMARY_GARBAGE_ROLE_ARN
  WK_E2E_BACKUP_SECONDARY_ENDPOINT WK_E2E_BACKUP_SECONDARY_REGION
  WK_E2E_BACKUP_SECONDARY_BUCKET WK_E2E_BACKUP_SECONDARY_PREFIX
  WK_E2E_BACKUP_SECONDARY_ACCESS_ROLE_ARN
  WK_E2E_BACKUP_SECONDARY_REPAIR_ROLE_ARN WK_E2E_BACKUP_SECONDARY_GARBAGE_ROLE_ARN
)
for variable in "${required[@]}"; do
  test -n "${!variable}" || { echo "::error::$variable is required"; exit 1; }
done
test "$WK_E2E_BACKUP_PROVIDER" = "aliyun"
test "$WK_E2E_BACKUP_OBJECT_LOCK_DAYS" -ge 7
WK_E2E_BINARY="$RUNNER_TEMP/wukongim-backup-production-e2e" timeout --signal=TERM --kill-after=30s 25m \
  go test -tags=e2e ./test/e2e/backup/three_node_restore \
    -run TestProductionStorageQualification -count=1 -timeout=23m -p=1 -v 2>&1 | tee "$LOG_FILE"
evidence_line="$(grep 'WK-BACKUP-PRODUCTION-EVIDENCE ' "$LOG_FILE" | tail -n 1)"
evidence_json="${evidence_line#*WK-BACKUP-PRODUCTION-EVIDENCE }"
jq -e \
  --arg schema "wukongim/backup-production-qualification/v3" \
  --arg provider "aliyun" \
  --arg key_authority "deployment-key-package/v1" \
  --arg run_id "$WK_E2E_BACKUP_RUN_ID" \
  --arg commit "$WK_E2E_BACKUP_COMMIT_SHA" \
  '(.schema == $schema) and
   (.provider == $provider) and
   (.key_authority == $key_authority) and
   (.run_id == $run_id) and
   (.commit == $commit) and
   (.primary_region != .secondary_region) and
   (.object_lock_days >= 7) and
   (.restored_messages > 0) and
   (.source_stopped == true) and
   (.fresh_target == true) and
   (.post_restore_write == true) and
   (.controller_fault == true) and
   (.slot_leader_fault == true) and
   (.data_node_fault == true) and
   (.restore_leader_fault == true) and
   (.repository_repair == true) and
   (.dual_corruption_rebase == true) and
   (.garbage_role_probe == true) and
   (.least_privilege_roles == true)' \
  >/dev/null <<<"$evidence_json"
`
	backupThreeNodeBuildCommand = `go build -tags=e2e \
  -ldflags="-X github.com/WuKongIM/WuKongIM/internal/app.backupQualifiedRevision=${GITHUB_SHA}" \
  -o "$RUNNER_TEMP/wukongim-backup-e2e" ./cmd/wukongim
`
	backupScaleBuildCommand = `go build -tags=e2e \
  -ldflags="-X github.com/WuKongIM/WuKongIM/internal/app.backupQualifiedRevision=${GITHUB_SHA}" \
  -o "$RUNNER_TEMP/wukongim-backup-scale-e2e" ./cmd/wukongim
`
	backupProductionBuildCommand = `go build -tags=e2e \
  -ldflags="-X github.com/WuKongIM/WuKongIM/internal/app.backupQualifiedRevision=${GITHUB_SHA}" \
  -o "$RUNNER_TEMP/wukongim-backup-production-e2e" ./cmd/wukongim
`
	backupProductionEvidenceCommand = `if [[ ! -f "$LOG_FILE" ]]; then
  printf '%s\n' 'backup production storage log was not created' > "$EVIDENCE_FILE"
  exit 0
fi
python3 - "$LOG_FILE" "$EVIDENCE_FILE" <<'PY'
import os
import pathlib
import sys

source = pathlib.Path(sys.argv[1])
target = pathlib.Path(sys.argv[2])
with source.open("rb") as stream:
    stream.seek(0, 2)
    size = stream.tell()
values = []
for name in (
    "ALIBABA_CLOUD_ACCESS_KEY_ID",
    "ALIBABA_CLOUD_ACCESS_KEY_SECRET",
    "WK_E2E_BACKUP_PRIMARY_ENDPOINT",
    "WK_E2E_BACKUP_PRIMARY_BUCKET",
    "WK_E2E_BACKUP_PRIMARY_PREFIX",
    "WK_E2E_BACKUP_PRIMARY_ACCESS_ROLE_ARN",
    "WK_E2E_BACKUP_PRIMARY_REPAIR_ROLE_ARN",
    "WK_E2E_BACKUP_PRIMARY_GARBAGE_ROLE_ARN",
    "WK_E2E_BACKUP_SECONDARY_ENDPOINT",
    "WK_E2E_BACKUP_SECONDARY_BUCKET",
    "WK_E2E_BACKUP_SECONDARY_PREFIX",
    "WK_E2E_BACKUP_SECONDARY_ACCESS_ROLE_ARN",
    "WK_E2E_BACKUP_SECONDARY_REPAIR_ROLE_ARN",
    "WK_E2E_BACKUP_SECONDARY_GARBAGE_ROLE_ARN",
):
    value = os.environ.get(name, "").encode()
    if value:
        values.append(value)
values = sorted(set(values), key=len, reverse=True)
overlap = max((len(value) for value in values), default=1) - 1
with source.open("rb") as stream:
    stream.seek(max(0, size - 1048576 - overlap))
    data = stream.read(1048576 + overlap)
marker = b"[REDACTED]"
for value in values:
    replacement = (marker * ((len(value) + len(marker) - 1) // len(marker)))[:len(value)]
    data = data.replace(value, replacement)
data = data[-1048576:]
target.write_bytes(data)
PY
`
	backupReleaseVerdictCommand = `umask 077
amd64_sha="$(awk '$2 == "wukongim-linux-amd64" {print $1}' "$QUALIFIED_DIR/SHA256SUMS")"
arm64_sha="$(awk '$2 == "wukongim-linux-arm64" {print $1}' "$QUALIFIED_DIR/SHA256SUMS")"
jq -n \
  --arg schema "wukongim/backup-release-qualification/v3" \
  --arg provider "aliyun" \
  --arg commit "$COMMIT_SHA" \
  --arg run_id "$RUN_ID" \
  --arg run_attempt "$RUN_ATTEMPT" \
  --arg run_url "$RUN_URL" \
  --arg amd64_sha "$amd64_sha" \
  --arg arm64_sha "$arm64_sha" \
  '{
    schema: $schema,
    provider: $provider,
    commit: $commit,
    run_id: $run_id,
    run_attempt: $run_attempt,
    run_url: $run_url,
    qualified_binaries: {
      linux_amd64_sha256: $amd64_sha,
      linux_arm64_sha256: $arm64_sha
    },
    gates: {
      portable_faults: "passed",
      three_node_recovery: "passed",
      scale_performance: "passed",
      production_storage: "passed",
      recorded_recovery_drill: "passed"
    }
  }' > "$EVIDENCE_FILE"
`
	backupQualifiedBinaryBuildCommand = `set -euo pipefail
mkdir -p "$QUALIFIED_DIR"
for architecture in amd64 arm64; do
  output="$QUALIFIED_DIR/wukongim-linux-$architecture"
  CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" go build \
    -trimpath \
    -ldflags="-s -w -X github.com/WuKongIM/WuKongIM/internal/app.backupQualifiedRevision=$COMMIT_SHA" \
    -o "$output" ./cmd/wukongim
  go version -m "$output" | grep -F "vcs.revision=$COMMIT_SHA"
done
(
  cd "$QUALIFIED_DIR"
  sha256sum wukongim-linux-amd64 wukongim-linux-arm64 \
    > SHA256SUMS
)
`
)

var expectedBackupQualificationJobs = map[string]ciJob{
	"portable-faults": {
		Name:           "Portable backup fault seams",
		RunsOn:         "ubuntu-24.04",
		TimeoutMinutes: 20,
		Env:            map[string]string{"GOWORK": "off"},
		Steps: []ciStep{
			checkoutStep(),
			setupGoStep(),
			verifyGoToolchainStep(),
			{
				Name:  "Run portable artifact and fault gates",
				Shell: "bash",
				Env: map[string]string{
					"LOG_FILE": "${{ runner.temp }}/backup-portable-faults.log",
				},
				Run: backupPortableFaultsCommand,
			},
			{
				Name:  "Prepare bounded portable failure evidence",
				If:    "failure()",
				Shell: "bash",
				Env: map[string]string{
					"LOG_FILE":      "${{ runner.temp }}/backup-portable-faults.log",
					"EVIDENCE_FILE": "${{ runner.temp }}/backup-portable-faults-failure.log",
				},
				Run: backupPortableEvidenceCommand,
			},
			{
				Name: "Upload bounded portable failure evidence",
				If:   "failure()",
				Uses: "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
				With: map[string]any{
					"name":              "backup-portable-faults-${{ github.run_id }}-${{ github.run_attempt }}",
					"path":              "${{ runner.temp }}/backup-portable-faults-failure.log",
					"if-no-files-found": "warn",
					"retention-days":    7,
				},
			},
		},
	},
	"three-node-backup": {
		Name:           "Three-node backup and restore",
		RunsOn:         "ubuntu-24.04",
		TimeoutMinutes: 25,
		Env:            map[string]string{"GOWORK": "off"},
		Steps: []ciStep{
			checkoutStep(),
			setupGoStep(),
			verifyGoToolchainStep(),
			{
				Name:  "Build e2e-tagged product binary",
				Shell: "bash",
				Run:   backupThreeNodeBuildCommand,
			},
			{
				Name:  "Run backup qualification scenarios",
				Shell: "bash",
				Env:   map[string]string{"LOG_FILE": "${{ runner.temp }}/backup-qualification.log"},
				Run:   backupQualificationCommand,
			},
			{
				Name:  "Prepare bounded failure evidence",
				If:    "failure()",
				Shell: "bash",
				Env: map[string]string{
					"LOG_FILE":      "${{ runner.temp }}/backup-qualification.log",
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
	"backup-scale-performance": {
		Name:           "Backup scale and SEND isolation",
		RunsOn:         "ubuntu-24.04",
		TimeoutMinutes: 30,
		Env: map[string]string{
			"GOWORK":                          "off",
			"WK_E2E_MEDIUM_RECIPIENT_HOTPATH": "1",
			"WK_E2E_MEDIUM_RECIPIENT_ENFORCE_ACCEPTANCE": "1",
			"WK_E2E_MEDIUM_RECIPIENT_CI_SCALE":           "1",
			"WK_E2E_MEDIUM_RECIPIENT_QPS":                "500",
			"WK_E2E_MEDIUM_RECIPIENT_ROUNDS":             "20",
			"WK_E2E_MEDIUM_BACKUP_QUALIFICATION":         "1",
		},
		Steps: []ciStep{
			checkoutStep(),
			setupGoStep(),
			verifyGoToolchainStep(),
			{
				Name:  "Build e2e-tagged product binary",
				Shell: "bash",
				Run:   backupScaleBuildCommand,
			},
			{
				Name:  "Run 256-Slot backup scale and latency gate",
				Shell: "bash",
				Env: map[string]string{
					"LOG_FILE": "${{ runner.temp }}/backup-scale-performance.log",
				},
				Run: backupScaleCommand,
			},
			{
				Name:  "Prepare bounded scale evidence",
				If:    "always()",
				Shell: "bash",
				Env: map[string]string{
					"LOG_FILE":      "${{ runner.temp }}/backup-scale-performance.log",
					"EVIDENCE_FILE": "${{ runner.temp }}/backup-scale-performance-evidence.log",
				},
				Run: backupScaleEvidenceCommand,
			},
			{
				Name: "Upload bounded scale evidence",
				If:   "always()",
				Uses: "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
				With: map[string]any{
					"name":              "backup-scale-performance-${{ github.run_id }}-${{ github.run_attempt }}",
					"path":              "${{ runner.temp }}/backup-scale-performance-evidence.log",
					"if-no-files-found": "error",
					"retention-days":    14,
				},
			},
		},
	},
	"production-storage": {
		Name:           "Production Alibaba OSS, deployment keys, and recovery drill",
		RunsOn:         "ubuntu-24.04",
		TimeoutMinutes: 30,
		Environment:    "backup-production",
		Env: map[string]string{
			"GOWORK": "off",
		},
		Steps: []ciStep{
			checkoutStep(),
			setupGoStep(),
			verifyGoToolchainStep(),
			{
				Name:  "Materialize protected deployment key credential",
				Shell: "bash",
				Env: map[string]string{
					"BACKUP_KEY_PACKAGE_B64": "${{ secrets.BACKUP_KEY_PACKAGE_B64 }}",
				},
				Run: backupKeyCredentialCommand,
			},
			{
				Name:  "Build production-provider e2e binary",
				Shell: "bash",
				Run:   backupProductionBuildCommand,
			},
			{
				Name:  "Run production storage recovery drill",
				Shell: "bash",
				Env: map[string]string{
					"LOG_FILE":                                 "${{ runner.temp }}/backup-production-storage.log",
					"ALIBABA_CLOUD_ACCESS_KEY_ID":              "${{ secrets.ALIBABA_CLOUD_ACCESS_KEY_ID }}",
					"ALIBABA_CLOUD_ACCESS_KEY_SECRET":          "${{ secrets.ALIBABA_CLOUD_ACCESS_KEY_SECRET }}",
					"WK_E2E_BACKUP_PRODUCTION":                 "1",
					"WK_E2E_BACKUP_PROVIDER":                   "aliyun",
					"WK_E2E_BACKUP_RUN_ID":                     "${{ github.run_id }}-${{ github.run_attempt }}",
					"WK_E2E_BACKUP_COMMIT_SHA":                 "${{ github.sha }}",
					"WK_E2E_BACKUP_REPOSITORY_ID":              "${{ vars.BACKUP_REPOSITORY_ID }}",
					"WK_E2E_BACKUP_SOURCE_GENERATION":          "source-${{ github.run_id }}-${{ github.run_attempt }}",
					"WK_E2E_BACKUP_TARGET_GENERATION":          "target-${{ github.run_id }}-${{ github.run_attempt }}",
					"WK_E2E_BACKUP_OBJECT_LOCK_DAYS":           "${{ vars.BACKUP_OBJECT_LOCK_DAYS }}",
					"WK_E2E_BACKUP_PRIMARY_ENDPOINT":           "${{ vars.BACKUP_PRIMARY_ENDPOINT }}",
					"WK_E2E_BACKUP_PRIMARY_REGION":             "${{ vars.BACKUP_PRIMARY_REGION }}",
					"WK_E2E_BACKUP_PRIMARY_BUCKET":             "${{ vars.BACKUP_PRIMARY_BUCKET }}",
					"WK_E2E_BACKUP_PRIMARY_PREFIX":             "${{ vars.BACKUP_PRIMARY_PREFIX }}/${{ github.run_id }}-${{ github.run_attempt }}",
					"WK_E2E_BACKUP_PRIMARY_ACCESS_ROLE_ARN":    "${{ vars.BACKUP_PRIMARY_ACCESS_ROLE_ARN }}",
					"WK_E2E_BACKUP_PRIMARY_REPAIR_ROLE_ARN":    "${{ vars.BACKUP_PRIMARY_REPAIR_ROLE_ARN }}",
					"WK_E2E_BACKUP_PRIMARY_GARBAGE_ROLE_ARN":   "${{ vars.BACKUP_PRIMARY_GARBAGE_ROLE_ARN }}",
					"WK_E2E_BACKUP_SECONDARY_ENDPOINT":         "${{ vars.BACKUP_SECONDARY_ENDPOINT }}",
					"WK_E2E_BACKUP_SECONDARY_REGION":           "${{ vars.BACKUP_SECONDARY_REGION }}",
					"WK_E2E_BACKUP_SECONDARY_BUCKET":           "${{ vars.BACKUP_SECONDARY_BUCKET }}",
					"WK_E2E_BACKUP_SECONDARY_PREFIX":           "${{ vars.BACKUP_SECONDARY_PREFIX }}/${{ github.run_id }}-${{ github.run_attempt }}",
					"WK_E2E_BACKUP_SECONDARY_ACCESS_ROLE_ARN":  "${{ vars.BACKUP_SECONDARY_ACCESS_ROLE_ARN }}",
					"WK_E2E_BACKUP_SECONDARY_REPAIR_ROLE_ARN":  "${{ vars.BACKUP_SECONDARY_REPAIR_ROLE_ARN }}",
					"WK_E2E_BACKUP_SECONDARY_GARBAGE_ROLE_ARN": "${{ vars.BACKUP_SECONDARY_GARBAGE_ROLE_ARN }}",
				},
				Run: backupProductionCommand,
			},
			{
				Name:  "Prepare bounded production evidence",
				If:    "always()",
				Shell: "bash",
				Env: map[string]string{
					"LOG_FILE":                                 "${{ runner.temp }}/backup-production-storage.log",
					"EVIDENCE_FILE":                            "${{ runner.temp }}/backup-production-storage-evidence.log",
					"ALIBABA_CLOUD_ACCESS_KEY_ID":              "${{ secrets.ALIBABA_CLOUD_ACCESS_KEY_ID }}",
					"ALIBABA_CLOUD_ACCESS_KEY_SECRET":          "${{ secrets.ALIBABA_CLOUD_ACCESS_KEY_SECRET }}",
					"WK_E2E_BACKUP_PRIMARY_ENDPOINT":           "${{ vars.BACKUP_PRIMARY_ENDPOINT }}",
					"WK_E2E_BACKUP_PRIMARY_BUCKET":             "${{ vars.BACKUP_PRIMARY_BUCKET }}",
					"WK_E2E_BACKUP_PRIMARY_PREFIX":             "${{ vars.BACKUP_PRIMARY_PREFIX }}/${{ github.run_id }}-${{ github.run_attempt }}",
					"WK_E2E_BACKUP_PRIMARY_ACCESS_ROLE_ARN":    "${{ vars.BACKUP_PRIMARY_ACCESS_ROLE_ARN }}",
					"WK_E2E_BACKUP_PRIMARY_REPAIR_ROLE_ARN":    "${{ vars.BACKUP_PRIMARY_REPAIR_ROLE_ARN }}",
					"WK_E2E_BACKUP_PRIMARY_GARBAGE_ROLE_ARN":   "${{ vars.BACKUP_PRIMARY_GARBAGE_ROLE_ARN }}",
					"WK_E2E_BACKUP_SECONDARY_ENDPOINT":         "${{ vars.BACKUP_SECONDARY_ENDPOINT }}",
					"WK_E2E_BACKUP_SECONDARY_BUCKET":           "${{ vars.BACKUP_SECONDARY_BUCKET }}",
					"WK_E2E_BACKUP_SECONDARY_PREFIX":           "${{ vars.BACKUP_SECONDARY_PREFIX }}/${{ github.run_id }}-${{ github.run_attempt }}",
					"WK_E2E_BACKUP_SECONDARY_ACCESS_ROLE_ARN":  "${{ vars.BACKUP_SECONDARY_ACCESS_ROLE_ARN }}",
					"WK_E2E_BACKUP_SECONDARY_REPAIR_ROLE_ARN":  "${{ vars.BACKUP_SECONDARY_REPAIR_ROLE_ARN }}",
					"WK_E2E_BACKUP_SECONDARY_GARBAGE_ROLE_ARN": "${{ vars.BACKUP_SECONDARY_GARBAGE_ROLE_ARN }}",
				},
				Run: backupProductionEvidenceCommand,
			},
			{
				Name: "Upload bounded production evidence",
				If:   "always()",
				Uses: "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
				With: map[string]any{
					"name":              "backup-production-storage-${{ github.run_id }}-${{ github.run_attempt }}",
					"path":              "${{ runner.temp }}/backup-production-storage-evidence.log",
					"if-no-files-found": "error",
					"retention-days":    30,
				},
			},
		},
	},
	"release-verdict": {
		Name: "Backup release qualification verdict",
		Needs: []string{
			"portable-faults",
			"three-node-backup",
			"backup-scale-performance",
			"production-storage",
		},
		RunsOn:         "ubuntu-24.04",
		TimeoutMinutes: 10,
		Steps: []ciStep{
			checkoutStep(),
			setupGoStep(),
			{
				Name:  "Build commit-bound qualified binaries",
				Shell: "bash",
				Env: map[string]string{
					"COMMIT_SHA":    "${{ github.sha }}",
					"QUALIFIED_DIR": "${{ runner.temp }}/backup-qualified",
				},
				Run: backupQualifiedBinaryBuildCommand,
			},
			{
				Name:  "Write recorded recovery drill verdict",
				Shell: "bash",
				Env: map[string]string{
					"EVIDENCE_FILE": "${{ runner.temp }}/backup-release-qualification.json",
					"COMMIT_SHA":    "${{ github.sha }}",
					"RUN_ID":        "${{ github.run_id }}",
					"RUN_ATTEMPT":   "${{ github.run_attempt }}",
					"RUN_URL":       "${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}",
					"QUALIFIED_DIR": "${{ runner.temp }}/backup-qualified",
				},
				Run: backupReleaseVerdictCommand,
			},
			{
				Name: "Upload commit-bound qualified binaries",
				Uses: "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
				With: map[string]any{
					"name":              "backup-qualified-binaries-${{ github.sha }}-${{ github.run_id }}-${{ github.run_attempt }}",
					"path":              "${{ runner.temp }}/backup-qualified",
					"if-no-files-found": "error",
					"retention-days":    90,
				},
			},
			{
				Name: "Upload release qualification verdict",
				Uses: "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
				With: map[string]any{
					"name":              "backup-release-qualification-${{ github.run_id }}-${{ github.run_attempt }}",
					"path":              "${{ runner.temp }}/backup-release-qualification.json",
					"if-no-files-found": "error",
					"retention-days":    90,
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

func TestLegacyAutomaticTestWorkflowsAreAbsent(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"ci.yml", "nightly.yml"} {
		path := filepath.Join(root, ".github", "workflows", name)
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s still exists; tests must be selected through the Agent validation protocol", name)
		} else if !os.IsNotExist(err) {
			t.Errorf("stat %s: %v", name, err)
		}
	}
}

func TestAgentPRValidationWorkflowContract(t *testing.T) {
	raw := readWorkflow(t, "agent-pr-validation.yml")
	if err := validateAgentPRValidationWorkflow(raw); err != nil {
		t.Fatal(err)
	}
}

func TestAgentPRValidationControlWorkflowContract(t *testing.T) {
	raw := readWorkflow(t, "agent-pr-validation-control.yml")
	if err := validateAgentPRValidationControlWorkflow(raw); err != nil {
		t.Fatal(err)
	}
}

func TestAgentPRValidationMergeGateWorkflowContract(t *testing.T) {
	raw := readWorkflow(t, "agent-pr-merge-gate.yml")
	if err := validateAgentPRValidationMergeGateWorkflow(raw); err != nil {
		t.Fatal(err)
	}
}

func TestAgentPRValidationMergeGateBootstrapTreeParsingFailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		treeJSON string
		wantGate string
		wantErr  bool
	}{
		{
			name:     "gate absent",
			treeJSON: `{"truncated":false,"tree":[]}`,
			wantGate: "false",
		},
		{
			name: "gate present",
			treeJSON: `{
  "truncated": false,
  "tree": [{"path": ".github/workflows/agent-pr-merge-gate.yml"}]
}`,
			wantGate: "true",
		},
		{
			name:     "tree null",
			treeJSON: `{"truncated":false,"tree":null}`,
			wantErr:  true,
		},
		{
			name:     "tree missing",
			treeJSON: `{"truncated":false}`,
			wantErr:  true,
		},
		{
			name:     "tree truncated",
			treeJSON: `{"truncated":true,"tree":[]}`,
			wantErr:  true,
		},
		{
			name:     "tree entry missing path",
			treeJSON: `{"truncated":false,"tree":[{}]}`,
			wantErr:  true,
		},
		{
			name:     "tree entry null path",
			treeJSON: `{"truncated":false,"tree":[{"path":null}]}`,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			treePath := filepath.Join(t.TempDir(), "base-tree.json")
			if err := os.WriteFile(treePath, []byte(tt.treeJSON), 0o600); err != nil {
				t.Fatalf("write tree fixture: %v", err)
			}
			schema := exec.Command(
				"jq",
				"-e",
				`.truncated == false and
(.tree | type == "array") and
all(.tree[];
  type == "object" and
  (.path | type == "string" and length > 0))`,
				treePath,
			)
			if output, err := schema.CombinedOutput(); err != nil {
				if tt.wantErr {
					return
				}
				t.Fatalf("validate tree schema: %v\n%s", err, output)
			}
			if tt.wantErr {
				t.Fatal("malformed bootstrap tree unexpectedly passed schema validation")
			}
			query := exec.Command(
				"jq",
				"-r",
				`any(.tree[]; .path == ".github/workflows/agent-pr-merge-gate.yml")`,
				treePath,
			)
			output, err := query.CombinedOutput()
			if err != nil {
				t.Fatalf("query gate path: %v\n%s", err, output)
			}
			if got := strings.TrimSpace(string(output)); got != tt.wantGate {
				t.Fatalf("base_has_gate = %q, want %q", got, tt.wantGate)
			}
		})
	}
}

func TestAgentPRValidationControlWorkflowRejectsReadOnlyActor(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation-control.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		"            admin|maintain|write) ;;",
		"            admin|maintain|write|read) ;;",
	)
	if err := validateAgentPRValidationControlWorkflow([]byte(mutated)); err == nil {
		t.Fatal("control validator accepted a read-only actor")
	}
}

func TestAgentPRValidationControlWorkflowRejectsMissingRequestStatus(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation-control.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		`-f "context=Agent Validation Request / PR #${PR_NUMBER} / Gate #${gate_run_id}"`,
		`-f "context=Unbound Agent Validation Request / PR #${PR_NUMBER} / Gate #${gate_run_id}"`,
	)
	if err := validateAgentPRValidationControlWorkflow([]byte(mutated)); err == nil {
		t.Fatal("control validator accepted dispatch without a commit-bound request status")
	}
}

func TestAgentWorkflowCatalogContract(t *testing.T) {
	root := repoRoot(t)
	catalog := readFile(t, filepath.Join(root, ".github", "workflows", "README.md"))
	agents := readFile(t, filepath.Join(root, "AGENTS.md"))
	codeowners := readFile(t, filepath.Join(root, ".github", "CODEOWNERS"))
	cloudRunbook := readFile(t, filepath.Join(root, "docs", "superpowers", "runbooks", "cloud-simulation.md"))

	workflows := map[string]string{
		"agent-pr-merge-gate.yml":         "Safety Automation - Agent PR Merge Gate",
		"agent-pr-validation.yml":         "Agent Tool - Validate PR",
		"agent-pr-validation-control.yml": "Safety Automation - Agent PR Validation Control",
		"backup-qualification.yml":        "Agent Tool - Qualify Backup",
		"cloud-sim-analyze.yml":           "Agent Tool - Analyze Cloud Simulation",
		"cloud-sim-cleanup.yml":           "Safety Automation - Reconcile Cloud Simulation Resources",
		"cloud-sim-monitor.yml":           "Safety Automation - Patrol Cloud Simulation Runs",
		"cloud-sim-oidc-subject.yml":      "Agent Tool - Configure Cloud Simulation OIDC Subject",
		"cloud-sim-provision.yml":         "Agent Tool - Provision Cloud Simulation",
	}
	for file, name := range workflows {
		raw := readFile(t, filepath.Join(root, ".github", "workflows", file))
		if !strings.HasPrefix(raw, "name: "+name+"\n") {
			t.Errorf("%s does not use cataloged name %q", file, name)
		}
		if !strings.Contains(catalog, "`"+file+"`") ||
			!strings.Contains(catalog, "`"+name+"`") {
			t.Errorf("workflow catalog does not map %s to %q", file, name)
		}
	}
	for _, removed := range []string{"ci.yml", "nightly.yml"} {
		if strings.Contains(catalog, "| `"+removed+"` |") {
			t.Errorf("workflow catalog still lists removed automatic test workflow %s", removed)
		}
	}
	for _, required := range []string{
		"agent-ci/docs-only",
		"agent-ci/go-fast",
		"agent-ci/web",
		"agent-ci/demo",
		"agent-ci/go-race",
		"agent-ci/go-integration",
		"agent-ci/go-e2e",
		"agent-ci/three-node-smoke",
		"agent-ci/run",
		"agent-validation-plan:v1",
		"retry_of_run_id",
		"Agent Validation Gate",
		"Agent Validation Evidence",
		"first_time_contributors",
	} {
		if !strings.Contains(catalog, required) {
			t.Errorf("workflow catalog is missing %q", required)
		}
	}
	for _, workflowPath := range []string{
		".github/workflows/cloud-sim-analyze.yml",
		".github/workflows/cloud-sim-cleanup.yml",
		".github/workflows/cloud-sim-oidc-subject.yml",
		".github/workflows/cloud-sim-provision.yml",
	} {
		if !strings.Contains(cloudRunbook, workflowPath) {
			t.Errorf("Cloud Simulation runbook is missing stable workflow path %q", workflowPath)
		}
	}
	for _, staleName := range []string{
		"Cloud Simulation - Configure OIDC Subject",
		"Cloud Simulation - Provision",
		"Cloud Simulation - Analysis Session",
		"Cloud Simulation - Cleanup",
	} {
		if strings.Contains(cloudRunbook, staleName) {
			t.Errorf("Cloud Simulation runbook still references stale display name %q", staleName)
		}
	}
	if !strings.Contains(agents, ".github/workflows/README.md") {
		t.Error("root AGENTS.md does not route Agents to the Workflow tool catalog")
	}
	for _, protected := range []string{
		"/.github/workflows/ @tangtaoit @No8blackball",
		"/.github/CODEOWNERS @tangtaoit @No8blackball",
		"/scripts/github_workflows_test.go @tangtaoit @No8blackball",
		"/scripts/agent-pr-validation-plan.sh @tangtaoit @No8blackball",
		"/scripts/agent_pr_validation_plan_test.go @tangtaoit @No8blackball",
	} {
		if !strings.Contains(codeowners, protected) {
			t.Errorf("CODEOWNERS is missing %q", protected)
		}
	}
}

func TestAgentWorkflowTriggerContract(t *testing.T) {
	root := repoRoot(t)
	paths, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("workflow inventory is empty")
	}
	for _, path := range paths {
		raw := readFile(t, path)
		var workflow struct {
			Name string               `yaml:"name"`
			On   map[string]yaml.Node `yaml:"on"`
		}
		if err := yaml.Unmarshal([]byte(raw), &workflow); err != nil {
			t.Errorf("%s: %v", filepath.Base(path), err)
			continue
		}
		if strings.HasPrefix(workflow.Name, "Agent Tool - ") {
			if len(workflow.On) != 1 {
				t.Errorf("%s Agent Tool triggers = %v, want one on-demand trigger", filepath.Base(path), workflow.On)
				continue
			}
			for trigger := range workflow.On {
				if trigger != "workflow_dispatch" && trigger != "repository_dispatch" {
					t.Errorf("%s Agent Tool uses automatic trigger %q", filepath.Base(path), trigger)
				}
			}
		}
		if _, scheduled := workflow.On["schedule"]; scheduled &&
			!strings.HasPrefix(workflow.Name, "Safety Automation - ") {
			t.Errorf("%s schedules work without a Safety Automation name", filepath.Base(path))
		}
	}
}

func TestAgentPRValidationWorkflowRejectsWritableTestJob(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		"  go-quality:\n    name: Agent / Go quality\n    if: needs.plan.outputs.go_fast == 'true'\n    needs: [plan, status-pending]\n    runs-on: ubuntu-24.04\n    timeout-minutes: 10\n    permissions:\n      contents: read",
		"  go-quality:\n    name: Agent / Go quality\n    if: needs.plan.outputs.go_fast == 'true'\n    needs: [plan, status-pending]\n    runs-on: ubuntu-24.04\n    timeout-minutes: 10\n    permissions:\n      contents: write",
	)
	if err := validateAgentPRValidationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted a writable PR test job")
	}
}

func TestAgentPRValidationWorkflowRejectsDefaultBranchTestCheckout(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		"  go-quality:\n    name: Agent / Go quality\n    if: needs.plan.outputs.go_fast == 'true'\n    needs: [plan, status-pending]\n    runs-on: ubuntu-24.04\n    timeout-minutes: 10\n    permissions:\n      contents: read\n    env:\n      GOWORK: \"off\"\n    steps:\n      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0\n        with:\n          ref: ${{ github.event.client_payload.merge_sha }}",
		"  go-quality:\n    name: Agent / Go quality\n    if: needs.plan.outputs.go_fast == 'true'\n    needs: [plan, status-pending]\n    runs-on: ubuntu-24.04\n    timeout-minutes: 10\n    permissions:\n      contents: read\n    env:\n      GOWORK: \"off\"\n    steps:\n      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0\n        with:\n          ref: main",
	)
	if err := validateAgentPRValidationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted a default-branch checkout for a PR test job")
	}
}

func TestAgentPRValidationWorkflowRejectsWritableDefaultBranchCache(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		"  go-quality:\n    name: Agent / Go quality\n    if: needs.plan.outputs.go_fast == 'true'\n    needs: [plan, status-pending]\n    runs-on: ubuntu-24.04\n    timeout-minutes: 10\n    permissions:\n      contents: read\n    env:\n      GOWORK: \"off\"\n    steps:\n      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0\n        with:\n          ref: ${{ github.event.client_payload.merge_sha }}\n          persist-credentials: false\n      - uses: actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6.5.0\n        with:\n          go-version-file: go.mod\n          cache: false",
		"  go-quality:\n    name: Agent / Go quality\n    if: needs.plan.outputs.go_fast == 'true'\n    needs: [plan, status-pending]\n    runs-on: ubuntu-24.04\n    timeout-minutes: 10\n    permissions:\n      contents: read\n    env:\n      GOWORK: \"off\"\n    steps:\n      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0\n        with:\n          ref: ${{ github.event.client_payload.merge_sha }}\n          persist-credentials: false\n      - uses: actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6.5.0\n        with:\n          go-version-file: go.mod\n          cache: true",
	)
	if err := validateAgentPRValidationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted a writable default-branch Go cache")
	}
}

func TestAgentPRValidationWorkflowRejectsUnconditionalTestJob(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		"  go-integration:\n    name: Agent / Go integration\n    if: needs.plan.outputs.go_integration == 'true'",
		"  go-integration:\n    name: Agent / Go integration\n    # if: needs.plan.outputs.go_integration == 'true'",
	)
	if err := validateAgentPRValidationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted an unconditional Agent test job")
	}
}

func TestAgentPRValidationWorkflowRejectsWritablePlanJob(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		"      statuses: read",
		"      statuses: write",
	)
	if err := validateAgentPRValidationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted a plan job that can write statuses")
	}
}

func TestAgentPRValidationWorkflowRejectsDeploymentEnvironment(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		"    timeout-minutes: 10\n    permissions:\n      contents: read\n    env:\n      GOWORK: \"off\"",
		"    timeout-minutes: 10\n    environment: production\n    permissions:\n      contents: read\n    env:\n      GOWORK: \"off\"",
	)
	if err := validateAgentPRValidationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted a deployment environment on a PR test job")
	}
}

func TestAgentPRValidationWorkflowRejectsSecretReference(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		"          GH_TOKEN: ${{ github.token }}",
		"          GH_TOKEN: ${{ secrets.PR_VALIDATION_TOKEN }}",
	)
	if err := validateAgentPRValidationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted a secret reference in the PR validation workflow")
	}
}

func TestAgentPRValidationWorkflowRejectsUnboundControlRun(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		`.path == ".github/workflows/agent-pr-validation-control.yml"`,
		`.path == ".github/workflows/ci.yml"`,
	)
	if err := validateAgentPRValidationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted a request that was not bound to the control workflow")
	}
}

func TestAgentPRValidationWorkflowRejectsReusableRequestStatus(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		`-f "context=Agent Validation Request / PR #${PR_NUMBER} / Gate #${GATE_RUN_ID}"`,
		`-f "context=Reusable Agent Validation Request / PR #${PR_NUMBER} / Gate #${GATE_RUN_ID}"`,
	)
	if err := validateAgentPRValidationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted a gate that does not consume the one-shot request")
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
		`          go build -tags=e2e \`,
		`          go build \`,
	)
	if err := validateBackupQualificationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted an untagged backup qualification binary")
	}
}

func TestBackupProductionEvidenceIsBoundedAndRedacted(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "production.log")
	evidencePath := filepath.Join(dir, "evidence.log")
	readBoundarySecret := "read-boundary-secret-that-must-not-leak-and-is-longest"
	tailBoundarySecret := "tail-boundary-secret-that-must-not-leak"
	nestedSecret := "secret-that-must-not-leak"
	nestedLongSecret := "role/" + nestedSecret + "/unique-suffix"
	overlap := len(readBoundarySecret) - 1
	total := 1048576 + overlap + 128
	readStart := total - 1048576 - overlap
	tailStart := total - 1048576
	body := bytes.Repeat([]byte("x"), total)
	copy(
		body[readStart-len(readBoundarySecret)/2:],
		[]byte(readBoundarySecret),
	)
	copy(
		body[tailStart-len(tailBoundarySecret)/2:],
		[]byte(tailBoundarySecret),
	)
	copy(body[tailStart+128:], []byte(nestedSecret))
	copy(body[tailStart+512:], []byte(nestedLongSecret))
	if err := os.WriteFile(logPath, body, 0o600); err != nil {
		t.Fatalf("write production log: %v", err)
	}
	command := exec.Command("bash", "-c", backupProductionEvidenceCommand)
	command.Env = append(os.Environ(),
		"LOG_FILE="+logPath,
		"EVIDENCE_FILE="+evidencePath,
		"ALIBABA_CLOUD_ACCESS_KEY_ID="+nestedSecret,
		"ALIBABA_CLOUD_ACCESS_KEY_SECRET="+readBoundarySecret,
		"WK_E2E_BACKUP_PRIMARY_PREFIX="+tailBoundarySecret,
		"WK_E2E_BACKUP_PRIMARY_ACCESS_ROLE_ARN="+nestedLongSecret,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("prepare production evidence: %v\n%s", err, output)
	}
	evidence, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatalf("read production evidence: %v", err)
	}
	if len(evidence) != 1048576 {
		t.Fatalf("production evidence size = %d, want 1048576", len(evidence))
	}
	for _, secret := range []string{
		readBoundarySecret, tailBoundarySecret, nestedSecret,
		nestedLongSecret,
	} {
		if bytes.Contains(evidence, []byte(secret)) {
			t.Fatalf("production evidence contains secret %q", secret)
		}
	}
	if bytes.Contains(
		evidence,
		[]byte(readBoundarySecret[len(readBoundarySecret)/2:]),
	) {
		t.Fatal("production evidence contains a partial secret crossing the read boundary")
	}
	if bytes.Contains(
		evidence,
		[]byte(tailBoundarySecret[len(tailBoundarySecret)/2:]),
	) {
		t.Fatal("production evidence contains a partial secret crossing the tail boundary")
	}
	if bytes.Contains(evidence, []byte("role/")) ||
		bytes.Contains(evidence, []byte("/unique-suffix")) {
		t.Fatal("production evidence contains a residual prefix or suffix from a nested secret")
	}
	if !bytes.Contains(evidence, []byte("[REDACTED]")) {
		t.Fatal("production evidence does not contain the redaction marker")
	}
}

func replaceWorkflowFirst(t *testing.T, workflow, old, replacement string) string {
	t.Helper()
	if !strings.Contains(workflow, old) {
		t.Fatalf("workflow mutation source is missing: %q", old)
	}
	return strings.Replace(workflow, old, replacement, 1)
}

func validateAgentPRValidationWorkflow(raw []byte) error {
	document, workflow, err := decodeWorkflow(raw)
	if err != nil {
		return err
	}
	if strings.Contains(string(raw), "secrets.") || strings.Contains(string(raw), "secrets[") {
		return fmt.Errorf("Agent validation workflow must not reference Secrets")
	}
	if strings.Contains(string(raw), "context=Agent Validation Gate") ||
		strings.Contains(string(raw), "context='Agent Validation Gate'") {
		return fmt.Errorf("Agent validation worker must publish evidence, not the PR merge-gate context")
	}
	if err := validateAllUses(document, raw); err != nil {
		return err
	}
	if workflow.Name != "Agent Tool - Validate PR" {
		return fmt.Errorf("workflow name = %q, want Agent Tool - Validate PR", workflow.Name)
	}
	if workflow.RunName != "Agent PR #${{ github.event.client_payload.pr_number }} validation head ${{ github.event.client_payload.head_sha }} merge ${{ github.event.client_payload.merge_sha }} gate ${{ github.event.client_payload.gate_run_id }} request ${{ github.event.client_payload.request_run_id }}" {
		return fmt.Errorf("workflow run-name does not identify the requested PR, head, test-merge, gate generation, and request")
	}
	if err := validateRepositoryDispatchTrigger(workflow.On); err != nil {
		return err
	}
	if len(workflow.Permissions) != 0 {
		return fmt.Errorf("Agent validation root permissions = %#v, want none", workflow.Permissions)
	}
	wantConcurrency := ciConcurrency{
		Group:            "agent-pr-validation-${{ github.event.client_payload.pr_number }}",
		CancelInProgress: boolPointer(true),
	}
	if !reflect.DeepEqual(workflow.Concurrency, wantConcurrency) {
		return fmt.Errorf("Agent validation concurrency = %#v, want %#v", workflow.Concurrency, wantConcurrency)
	}
	jobNames := []string{
		"plan",
		"status-pending",
		"go-quality",
		"go-unit",
		"web",
		"demo",
		"go-race",
		"go-integration",
		"go-e2e",
		"three-node-smoke",
		"gate",
	}
	root := document.Content[0]
	if err := validateMappingKeys(
		root,
		[]string{"name", "run-name", "on", "permissions", "concurrency", "jobs"},
		"Agent validation workflow root",
	); err != nil {
		return err
	}
	jobs, ok := mappingValue(root, "jobs")
	if !ok {
		return fmt.Errorf("Agent validation workflow jobs are missing")
	}
	if err := validateMappingKeys(jobs, jobNames, "Agent validation workflow jobs"); err != nil {
		return err
	}
	for _, name := range jobNames {
		if workflow.Jobs[name].Environment != "" {
			return fmt.Errorf("Agent validation job %q must not use a deployment environment", name)
		}
	}
	plan := workflow.Jobs["plan"]
	if !reflect.DeepEqual(plan.Permissions, map[string]string{
		"actions":       "read",
		"contents":      "read",
		"issues":        "read",
		"pull-requests": "read",
		"statuses":      "read",
	}) {
		return fmt.Errorf("Agent validation plan permissions = %#v", plan.Permissions)
	}
	wantPlanOutputs := map[string]string{
		"docs_only":        "${{ steps.plan.outputs.docs_only }}",
		"go_fast":          "${{ steps.plan.outputs.go_fast }}",
		"web":              "${{ steps.plan.outputs.web }}",
		"demo":             "${{ steps.plan.outputs.demo }}",
		"go_race":          "${{ steps.plan.outputs.go_race }}",
		"go_integration":   "${{ steps.plan.outputs.go_integration }}",
		"go_e2e":           "${{ steps.plan.outputs.go_e2e }}",
		"three_node_smoke": "${{ steps.plan.outputs.three_node_smoke }}",
		"plan_comment_id":  "${{ steps.plan.outputs.plan_comment_id }}",
		"retry_of_run_id":  "${{ steps.plan.outputs.retry_of_run_id }}",
	}
	if !reflect.DeepEqual(plan.Outputs, wantPlanOutputs) {
		return fmt.Errorf("Agent validation plan outputs = %#v, want %#v", plan.Outputs, wantPlanOutputs)
	}
	var planCheckout *ciStep
	for index := range plan.Steps {
		if strings.HasPrefix(plan.Steps[index].Uses, "actions/checkout@") {
			if planCheckout != nil {
				return fmt.Errorf("Agent validation plan contains multiple checkout steps")
			}
			planCheckout = &plan.Steps[index]
		}
	}
	if planCheckout == nil {
		return fmt.Errorf("Agent validation plan has no default-branch checkout")
	}
	wantPlanCheckout := map[string]any{
		"ref":                 "${{ github.event.repository.default_branch }}",
		"persist-credentials": false,
	}
	if !reflect.DeepEqual(planCheckout.With, wantPlanCheckout) {
		return fmt.Errorf("Agent validation plan checkout = %#v, want %#v", planCheckout.With, wantPlanCheckout)
	}
	var planScript strings.Builder
	for _, step := range plan.Steps {
		planScript.WriteString(step.Run)
		planScript.WriteByte('\n')
	}
	for _, required := range []string{
		`actions/runs/${REQUEST_RUN_ID}`,
		`.path == ".github/workflows/agent-pr-validation-control.yml"`,
		`.event == "pull_request_target"`,
		`.display_title == $title`,
		`validation labeled head ${EXPECTED_HEAD_SHA} merge ${EXPECTED_MERGE_SHA}`,
		`.actor.login == $actor`,
		`actions/runs/${GATE_RUN_ID}`,
		`.path == ".github/workflows/agent-pr-merge-gate.yml"`,
		`.conclusion == "failure"`,
		`Agent Validation Request / PR #${PR_NUMBER} / Gate #${GATE_RUN_ID}`,
		`select(.context == $context)`,
		`endswith($suffix)`,
		`test "$current_merge_sha" = "$EXPECTED_MERGE_SHA"`,
		`"$TRIGGER_ACTOR" "$EXPECTED_HEAD_SHA" "$EXPECTED_MERGE_SHA"`,
		`"$GATE_RUN_ID"`,
	} {
		if !strings.Contains(planScript.String(), required) {
			return fmt.Errorf("Agent validation plan is missing request binding %q", required)
		}
	}
	pending := workflow.Jobs["status-pending"]
	if !reflect.DeepEqual(pending.Needs, []string{"plan"}) {
		return fmt.Errorf("Agent validation pending-status needs = %#v, want plan", pending.Needs)
	}
	if !reflect.DeepEqual(pending.Permissions, map[string]string{
		"pull-requests": "read",
		"statuses":      "write",
	}) {
		return fmt.Errorf("Agent validation pending-status permissions = %#v", pending.Permissions)
	}
	testConditions := map[string]string{
		"go-quality":       "needs.plan.outputs.go_fast == 'true'",
		"go-unit":          "needs.plan.outputs.go_fast == 'true'",
		"web":              "needs.plan.outputs.web == 'true'",
		"demo":             "needs.plan.outputs.demo == 'true'",
		"go-race":          "needs.plan.outputs.go_race == 'true'",
		"go-integration":   "needs.plan.outputs.go_integration == 'true'",
		"go-e2e":           "needs.plan.outputs.go_e2e == 'true'",
		"three-node-smoke": "needs.plan.outputs.three_node_smoke == 'true'",
	}
	for name, condition := range testConditions {
		job := workflow.Jobs[name]
		if job.If != condition {
			return fmt.Errorf("Agent validation test job %q condition = %q, want %q", name, job.If, condition)
		}
		if !reflect.DeepEqual(job.Needs, []string{"plan", "status-pending"}) {
			return fmt.Errorf("Agent validation test job %q needs = %#v", name, job.Needs)
		}
		if !reflect.DeepEqual(job.Permissions, map[string]string{"contents": "read"}) {
			return fmt.Errorf("Agent validation test job %q permissions = %#v, want contents: read", name, job.Permissions)
		}
		var checkout *ciStep
		for index := range job.Steps {
			if strings.HasPrefix(job.Steps[index].Uses, "actions/checkout@") {
				if checkout != nil {
					return fmt.Errorf("Agent validation test job %q contains multiple checkout steps", name)
				}
				checkout = &job.Steps[index]
			}
		}
		if checkout == nil {
			return fmt.Errorf("Agent validation test job %q has no checkout step", name)
		}
		wantCheckout := map[string]any{
			"ref":                 "${{ github.event.client_payload.merge_sha }}",
			"persist-credentials": false,
		}
		if !reflect.DeepEqual(checkout.With, wantCheckout) {
			return fmt.Errorf("Agent validation test job %q checkout = %#v, want %#v", name, checkout.With, wantCheckout)
		}
		if name != "web" && name != "demo" {
			var setupGo *ciStep
			for index := range job.Steps {
				if strings.HasPrefix(job.Steps[index].Uses, "actions/setup-go@") {
					setupGo = &job.Steps[index]
					break
				}
			}
			if setupGo == nil {
				return fmt.Errorf("Agent validation Go job %q has no setup-go step", name)
			}
			wantSetupGo := map[string]any{
				"go-version-file": "go.mod",
				"cache":           false,
			}
			if !reflect.DeepEqual(setupGo.With, wantSetupGo) {
				return fmt.Errorf("Agent validation Go job %q setup-go = %#v, want %#v", name, setupGo.With, wantSetupGo)
			}
		}
	}
	gate, ok := workflow.Jobs["gate"]
	if !ok {
		return fmt.Errorf("Agent validation workflow gate job is missing")
	}
	if gate.Name != "Publish Agent validation evidence" {
		return fmt.Errorf("Agent validation evidence publisher name = %q", gate.Name)
	}
	if gate.If != "always()" {
		return fmt.Errorf("Agent validation gate must run with always()")
	}
	wantGateNeeds := jobNames[:len(jobNames)-1]
	if !reflect.DeepEqual(gate.Needs, wantGateNeeds) {
		return fmt.Errorf("Agent validation gate needs = %#v, want %#v", gate.Needs, wantGateNeeds)
	}
	if !reflect.DeepEqual(gate.Permissions, map[string]string{
		"actions":       "write",
		"pull-requests": "read",
		"statuses":      "write",
	}) {
		return fmt.Errorf("Agent validation gate permissions are not fail-closed")
	}
	for _, name := range []string{"status-pending", "gate"} {
		for _, step := range workflow.Jobs[name].Steps {
			if strings.HasPrefix(step.Uses, "actions/checkout@") {
				return fmt.Errorf("Agent validation status job %q must not checkout code", name)
			}
		}
	}
	var gateScript strings.Builder
	for _, step := range gate.Steps {
		gateScript.WriteString(step.Run)
		gateScript.WriteByte('\n')
	}
	for _, required := range []string{
		`context=Agent Validation Request / PR #${PR_NUMBER} / Gate #${GATE_RUN_ID}`,
		`target_url="$REQUEST_RUN_URL"`,
		`context=Agent Validation Evidence / PR #${PR_NUMBER} / Gate #${GATE_RUN_ID}`,
		`publish_handoff_error`,
		`state=error`,
		`"$current_head" != "$HEAD_SHA" || "$current_merge" != "$MERGE_SHA"`,
		`latest_gate_run_id`,
		`"$latest_gate_run_id" != "$GATE_RUN_ID"`,
		`should_rerun_gate=false`,
		`actions/runs/${GATE_RUN_ID}/rerun`,
	} {
		if !strings.Contains(gateScript.String(), required) {
			return fmt.Errorf("Agent validation gate is missing request consumption %q", required)
		}
	}
	return nil
}

func validateAgentPRValidationControlWorkflow(raw []byte) error {
	document, workflow, err := decodeWorkflow(raw)
	if err != nil {
		return err
	}
	if strings.Contains(string(raw), "secrets.") || strings.Contains(string(raw), "secrets[") {
		return fmt.Errorf("control workflow must not reference Secrets")
	}
	if workflow.Name != "Safety Automation - Agent PR Validation Control" {
		return fmt.Errorf("control workflow name = %q", workflow.Name)
	}
	if workflow.RunName != "Agent PR #${{ github.event.pull_request.number }} validation ${{ github.event.action }} head ${{ github.event.pull_request.head.sha }} merge ${{ github.event.pull_request.merge_commit_sha }}" {
		return fmt.Errorf("control workflow run-name does not identify the PR event, head, and test-merge")
	}
	if err := validateAgentControlTriggers(workflow.On); err != nil {
		return err
	}
	if len(workflow.Permissions) != 0 {
		return fmt.Errorf("control workflow root permissions = %#v, want none", workflow.Permissions)
	}
	root := document.Content[0]
	if err := validateMappingKeys(
		root,
		[]string{"name", "run-name", "on", "permissions", "jobs"},
		"Agent validation control workflow root",
	); err != nil {
		return err
	}
	jobs, ok := mappingValue(root, "jobs")
	if !ok {
		return fmt.Errorf("Agent validation control jobs are missing")
	}
	if err := validateMappingKeys(jobs, []string{"request", "invalidate"}, "Agent validation control jobs"); err != nil {
		return err
	}
	request := workflow.Jobs["request"]
	if request.Environment != "" {
		return fmt.Errorf("Agent validation request must not use a deployment environment")
	}
	if request.If != "github.event.action == 'labeled' && github.event.label.name == 'agent-ci/run'" {
		return fmt.Errorf("Agent validation request condition is not bound to agent-ci/run")
	}
	if !reflect.DeepEqual(request.Permissions, map[string]string{
		"actions":       "read",
		"contents":      "write",
		"pull-requests": "read",
		"statuses":      "write",
	}) {
		return fmt.Errorf("Agent validation request permissions = %#v", request.Permissions)
	}
	var requestScript strings.Builder
	for _, step := range request.Steps {
		requestScript.WriteString(step.Run)
		requestScript.WriteByte('\n')
	}
	for _, required := range []string{
		`test "$current_head" = "$HEAD_SHA"`,
		`test "$merge_sha" = "$EVENT_MERGE_SHA"`,
		`.merge_commit_sha`,
		`actions/workflows/agent-pr-merge-gate.yml/runs`,
		`.conclusion == "failure"`,
		`collaborators/${TRIGGER_ACTOR}/permission`,
		"admin|maintain|write) ;;",
		`context=Agent Validation Request / PR #${PR_NUMBER} / Gate #${gate_run_id}`,
		`target_url="$REQUEST_RUN_URL"`,
		"--arg event_type agent-pr-validation",
		"--arg merge_sha \"$merge_sha\"",
		"--arg gate_run_id \"$gate_run_id\"",
		"--arg request_run_id \"$REQUEST_RUN_ID\"",
		`repos/${GITHUB_REPOSITORY}/dispatches`,
	} {
		if !strings.Contains(requestScript.String(), required) {
			return fmt.Errorf("Agent validation request is missing trusted dispatch contract %q", required)
		}
	}
	invalidate := workflow.Jobs["invalidate"]
	if invalidate.Environment != "" {
		return fmt.Errorf("Agent validation invalidation must not use a deployment environment")
	}
	if invalidate.If != "github.event.action == 'edited' || github.event.action == 'opened' || github.event.action == 'reopened' || github.event.action == 'synchronize'" {
		return fmt.Errorf("Agent validation invalidation condition does not cover edited, opened, reopened, and synchronize")
	}
	wantConcurrency := &ciConcurrency{
		Group:            "agent-pr-validation-${{ github.event.pull_request.number }}",
		CancelInProgress: boolPointer(true),
	}
	if !reflect.DeepEqual(invalidate.Concurrency, wantConcurrency) {
		return fmt.Errorf("Agent validation invalidation concurrency = %#v, want %#v", invalidate.Concurrency, wantConcurrency)
	}
	if !reflect.DeepEqual(invalidate.Permissions, map[string]string{"statuses": "write"}) {
		return fmt.Errorf("Agent validation invalidation permissions = %#v, want statuses: write", invalidate.Permissions)
	}
	var invalidateScript strings.Builder
	for _, step := range invalidate.Steps {
		invalidateScript.WriteString(step.Run)
		invalidateScript.WriteByte('\n')
	}
	for _, required := range []string{
		`context=Agent Validation Request / PR #${PR_NUMBER}`,
		`state=failure`,
		`target_url="$INVALIDATION_RUN_URL"`,
	} {
		if !strings.Contains(invalidateScript.String(), required) {
			return fmt.Errorf("Agent validation invalidation is missing %q", required)
		}
	}
	if strings.Contains(string(raw), "actions/checkout") ||
		strings.Contains(string(raw), "github.event.pull_request.head.repo") {
		return fmt.Errorf("control workflow must never checkout pull request code")
	}
	return nil
}

func validateAgentPRValidationMergeGateWorkflow(raw []byte) error {
	document, workflow, err := decodeWorkflow(raw)
	if err != nil {
		return err
	}
	if strings.Contains(string(raw), "secrets.") ||
		strings.Contains(string(raw), "secrets[") ||
		strings.Contains(string(raw), "actions/checkout") {
		return fmt.Errorf("Agent PR merge gate must not use Secrets or checkout code")
	}
	if workflow.Name != "Safety Automation - Agent PR Merge Gate" {
		return fmt.Errorf("Agent PR merge gate workflow name = %q", workflow.Name)
	}
	if workflow.RunName != "Agent PR #${{ github.event.pull_request.number }} merge gate ${{ github.event.action }} head ${{ github.event.pull_request.head.sha }} merge ${{ github.sha }}" {
		return fmt.Errorf("Agent PR merge gate run-name is not PR, head, and test-merge bound")
	}
	if !strings.Contains(string(raw), "MERGE_SHA: ${{ github.sha }}") {
		return fmt.Errorf("Agent PR merge gate does not bind MERGE_SHA to github.sha")
	}
	if !strings.Contains(string(raw), "BASE_SHA: ${{ github.event.pull_request.base.sha }}") {
		return fmt.Errorf("Agent PR merge gate does not bind BASE_SHA to the PR base")
	}
	if err := validateAgentMergeGateTriggers(workflow.On); err != nil {
		return err
	}
	if len(workflow.Permissions) != 0 {
		return fmt.Errorf("Agent PR merge gate root permissions = %#v, want none", workflow.Permissions)
	}
	wantConcurrency := ciConcurrency{
		Group:            "agent-pr-merge-gate-${{ github.event.pull_request.number }}-${{ github.run_id }}",
		CancelInProgress: boolPointer(true),
	}
	if !reflect.DeepEqual(workflow.Concurrency, wantConcurrency) {
		return fmt.Errorf("Agent PR merge gate concurrency = %#v, want %#v", workflow.Concurrency, wantConcurrency)
	}
	root := document.Content[0]
	if err := validateMappingKeys(
		root,
		[]string{"name", "run-name", "on", "permissions", "concurrency", "jobs"},
		"Agent PR merge gate workflow root",
	); err != nil {
		return err
	}
	jobs, ok := mappingValue(root, "jobs")
	if !ok {
		return fmt.Errorf("Agent PR merge gate jobs are missing")
	}
	if err := validateMappingKeys(jobs, []string{"gate"}, "Agent PR merge gate jobs"); err != nil {
		return err
	}
	gate := workflow.Jobs["gate"]
	if gate.Name != "Agent Validation Gate" ||
		gate.If != "" ||
		gate.RunsOn != "ubuntu-24.04" ||
		gate.TimeoutMinutes != 3 ||
		gate.Environment != "" {
		return fmt.Errorf("Agent PR merge gate job does not match the stable fail-closed contract")
	}
	if !reflect.DeepEqual(gate.Permissions, map[string]string{
		"actions":       "read",
		"contents":      "read",
		"pull-requests": "read",
		"statuses":      "read",
	}) {
		return fmt.Errorf("Agent PR merge gate permissions = %#v, want read-only evidence access", gate.Permissions)
	}
	if len(gate.Steps) != 1 || gate.Steps[0].Uses != "" {
		return fmt.Errorf("Agent PR merge gate must contain one script-only verification step")
	}
	script := gate.Steps[0].Run
	for _, required := range []string{
		`"$RUN_ATTEMPT" -eq 1`,
		`git/commits/${BASE_SHA}`,
		`.truncated == false`,
		`.tree | type == "array"`,
		`all(.tree[];`,
		`.path | type == "string" and length > 0`,
		`base_has_gate`,
		`test "$base_has_gate" = true`,
		`.github/workflows/agent-pr-merge-gate.yml`,
		`Bootstrap PR: the merge-gate workflow is not yet on the base branch`,
		`[[ "$MERGE_SHA" =~ ^[0-9a-f]{40}$ ]]`,
		`[[ "$GATE_RUN_ID" =~ ^[1-9][0-9]{0,19}$ ]]`,
		`test "$current_head" = "$HEAD_SHA"`,
		`test "$current_merge" = "$MERGE_SHA"`,
		`Agent Validation Request / PR #${PR_NUMBER} / Gate #${GATE_RUN_ID}`,
		`Agent Validation Evidence / PR #${PR_NUMBER} / Gate #${GATE_RUN_ID}`,
		`.created_at >= $event_updated_at`,
		`.path == ".github/workflows/agent-pr-validation-control.yml"`,
		`.event == "pull_request_target"`,
		`Agent PR #${PR_NUMBER} validation labeled head ${HEAD_SHA} merge ${MERGE_SHA}`,
		`.path == ".github/workflows/agent-pr-validation.yml"`,
		`.event == "repository_dispatch"`,
		`validation head ${HEAD_SHA} merge ${MERGE_SHA} gate ${GATE_RUN_ID} request ${request_run_id}`,
		`for attempt in {1..12}`,
		`test "$evidence_complete" = true`,
		`.status == "completed"`,
		`.conclusion == "success"`,
		`.state == "success"`,
	} {
		if !strings.Contains(script, required) {
			return fmt.Errorf("Agent PR merge gate is missing binding %q", required)
		}
	}
	if strings.Count(script, "verify_latest_gate_generation") < 3 {
		return fmt.Errorf("Agent PR merge gate must verify the latest generation before and after evidence checks")
	}
	return nil
}

func validateBackupQualificationWorkflow(raw []byte) error {
	return validateExpectedWorkflow(
		raw,
		"Agent Tool - Qualify Backup",
		[]string{
			"portable-faults",
			"three-node-backup",
			"backup-scale-performance",
			"production-storage",
			"release-verdict",
		},
		expectedBackupQualificationJobs,
	)
}

func validateExpectedWorkflow(
	raw []byte,
	wantName string,
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
	if err := validateManualOnlyTriggers(workflow.On); err != nil {
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
	if job.If != "" {
		keys = append(keys, "if")
	}
	if job.Environment != "" {
		keys = append(keys, "environment")
	}
	if job.Needs != nil {
		keys = append(keys, "needs")
	}
	if job.Permissions != nil {
		keys = append(keys, "permissions")
	}
	if job.Outputs != nil {
		keys = append(keys, "outputs")
	}
	if job.Concurrency != nil {
		keys = append(keys, "concurrency")
	}
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
	if step.ID != "" {
		keys = append(keys, "id")
	}
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

func validateRepositoryDispatchTrigger(triggers map[string]yaml.Node) error {
	if len(triggers) != 1 {
		return fmt.Errorf("Agent validation trigger keys = %d, want exactly repository_dispatch", len(triggers))
	}
	trigger, ok := triggers["repository_dispatch"]
	if !ok {
		return fmt.Errorf("Agent validation workflow trigger repository_dispatch is missing")
	}
	if err := validateMappingKeys(&trigger, []string{"types"}, "Agent validation repository_dispatch trigger"); err != nil {
		return err
	}
	types, ok := mappingValue(&trigger, "types")
	if !ok || types.Kind != yaml.SequenceNode || len(types.Content) != 1 ||
		types.Content[0].Value != "agent-pr-validation" {
		return fmt.Errorf("Agent validation repository_dispatch types must be exactly [agent-pr-validation]")
	}
	return nil
}

func validateAgentControlTriggers(triggers map[string]yaml.Node) error {
	if len(triggers) != 1 {
		return fmt.Errorf("Agent validation control trigger keys = %d, want exactly pull_request_target", len(triggers))
	}
	trigger, ok := triggers["pull_request_target"]
	if !ok {
		return fmt.Errorf("Agent validation control trigger pull_request_target is missing")
	}
	if err := validateMappingKeys(&trigger, []string{"types"}, "Agent validation pull_request_target trigger"); err != nil {
		return err
	}
	types, ok := mappingValue(&trigger, "types")
	if !ok || types.Kind != yaml.SequenceNode || len(types.Content) != 5 ||
		types.Content[0].Value != "edited" ||
		types.Content[1].Value != "labeled" ||
		types.Content[2].Value != "opened" ||
		types.Content[3].Value != "reopened" ||
		types.Content[4].Value != "synchronize" {
		return fmt.Errorf("Agent validation pull_request_target types must be exactly [edited, labeled, opened, reopened, synchronize]")
	}
	return nil
}

func validateAgentMergeGateTriggers(triggers map[string]yaml.Node) error {
	if len(triggers) != 1 {
		return fmt.Errorf("Agent PR merge gate trigger keys = %d, want exactly pull_request", len(triggers))
	}
	trigger, ok := triggers["pull_request"]
	if !ok {
		return fmt.Errorf("Agent PR merge gate pull_request trigger is missing")
	}
	if err := validateMappingKeys(&trigger, []string{"types"}, "Agent PR merge gate pull_request trigger"); err != nil {
		return err
	}
	types, ok := mappingValue(&trigger, "types")
	if !ok || types.Kind != yaml.SequenceNode || len(types.Content) != 4 ||
		types.Content[0].Value != "edited" ||
		types.Content[1].Value != "opened" ||
		types.Content[2].Value != "reopened" ||
		types.Content[3].Value != "synchronize" {
		return fmt.Errorf("Agent PR merge gate pull_request types must be exactly [edited, opened, reopened, synchronize]")
	}
	return nil
}

func validateManualOnlyTriggers(triggers map[string]yaml.Node) error {
	if len(triggers) != 1 {
		return fmt.Errorf("workflow trigger keys = %d, want exactly workflow_dispatch", len(triggers))
	}
	workflowDispatch, ok := triggers["workflow_dispatch"]
	if !ok {
		return fmt.Errorf("workflow trigger %q is missing", "workflow_dispatch")
	}
	if !isEmptyTrigger(workflowDispatch) {
		return fmt.Errorf("workflow trigger %q must not contain inputs or options", "workflow_dispatch")
	}
	return nil
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
