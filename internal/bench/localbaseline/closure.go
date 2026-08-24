package localbaseline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

const (
	// StepClosureSchema identifies one verified step whose raw payload,
	// reconstructed typed evidence, and closed decision are bound together.
	StepClosureSchema = "wukongim/chat-lifecycle-local-single-node-step-closure/v1"
	// StepClosureManifestSchema identifies the checksum inventory that closes
	// the raw manifest together with its derived evidence and decision.
	StepClosureManifestSchema = "wukongim/chat-lifecycle-local-single-node-step-closure-manifest/v1"
)

// StepClosure is the only rate-step shape accepted by the baseline gate. The
// filesystem verifier constructs it after rebuilding Evidence from the raw
// payload named by ClosureManifest and comparing Result byte-for-byte by value.
type StepClosure struct {
	Schema                string           `json:"schema"`
	ClosureManifest       string           `json:"closure_manifest"`
	ClosureManifestSHA256 string           `json:"closure_manifest_sha256"`
	Evidence              StepEvidence     `json:"evidence"`
	Result                ClosedStepResult `json:"result"`
}

// StepClosureManifest is a bounded relative-path map. PayloadManifest points
// to the raw payload inventory; Evidence and Result point to the two derived
// documents that were written from the verified raw bytes.
type StepClosureManifest struct {
	Schema          string              `json:"schema"`
	PayloadManifest string              `json:"payload_manifest"`
	PayloadSHA256   string              `json:"payload_manifest_sha256"`
	Inputs          StepClosureInputs   `json:"inputs"`
	Settings        StepClosureSettings `json:"settings"`
	Evidence        string              `json:"evidence"`
	EvidenceSHA256  string              `json:"evidence_sha256"`
	Result          string              `json:"result"`
	ResultSHA256    string              `json:"result_sha256"`
}

// StepClosureInputs names the complete raw, checksummed input set needed to
// deterministically rebuild a typed step.
type StepClosureInputs struct {
	Scenario          string `json:"scenario"`
	Plan              string `json:"plan"`
	Report            string `json:"report"`
	EffectiveConfig   string `json:"effective_config"`
	WukongIMBinary    string `json:"wukongim_binary"`
	WkbenchBinary     string `json:"wkbench_binary"`
	ProductExecutable string `json:"product_executable"`
	DiagnosticSummary string `json:"diagnostic_summary"`
	Lifecycle         string `json:"lifecycle"`
	PostWarmupMetrics string `json:"post_warmup_metrics"`
	TerminalMetrics   string `json:"terminal_metrics"`
	StorageOverlap    string `json:"storage_overlap"`
	StorageSummary    string `json:"storage_summary"`
	HostIOSummary     string `json:"host_io_summary"`
	ProfileStatus     string `json:"profile_status"`
}

// StepClosureSettings binds CLI settings that are not derivable from a raw
// coordinator report but materially affect classification.
type StepClosureSettings struct {
	OfferedSendQPS               int     `json:"offered_send_qps"`
	RequiredActiveConnections    int     `json:"required_active_connections"`
	ConfiguredGroupMembers       int     `json:"configured_group_members"`
	ConfiguredWarmupSeconds      int     `json:"configured_warmup_seconds"`
	ConfiguredMeasuredSeconds    int     `json:"configured_measured_seconds"`
	ConfiguredDrainBudgetSeconds int     `json:"configured_drain_budget_seconds"`
	MaximumSampleGapSeconds      float64 `json:"maximum_sample_gap_seconds"`
}

// ValidateStepClosureManifest rejects unsafe paths and malformed digests
// before a command opens any referenced artifact.
func ValidateStepClosureManifest(manifest StepClosureManifest) bool {
	return manifest.Schema == StepClosureManifestSchema &&
		validRelativeArtifactPath(manifest.PayloadManifest) && validLowerDigest(manifest.PayloadSHA256) &&
		validRelativeArtifactPath(manifest.Inputs.Scenario) && validRelativeArtifactPath(manifest.Inputs.Plan) &&
		validRelativeArtifactPath(manifest.Inputs.Report) &&
		manifest.Inputs.EffectiveConfig == "config/effective-wukongim.toml" &&
		manifest.Inputs.WukongIMBinary == "bin/wukongim" &&
		manifest.Inputs.WkbenchBinary == "bin/wkbench" &&
		manifest.Inputs.ProductExecutable == fmt.Sprintf("reports/%06d-qps/evidence/product-executable.tsv", manifest.Settings.OfferedSendQPS) &&
		validRelativeArtifactPath(manifest.Inputs.DiagnosticSummary) && validRelativeArtifactPath(manifest.Inputs.Lifecycle) &&
		validRelativeArtifactPath(manifest.Inputs.PostWarmupMetrics) && validRelativeArtifactPath(manifest.Inputs.TerminalMetrics) &&
		validRelativeArtifactPath(manifest.Inputs.StorageOverlap) && validRelativeArtifactPath(manifest.Inputs.StorageSummary) &&
		validRelativeArtifactPath(manifest.Inputs.HostIOSummary) && validRelativeArtifactPath(manifest.Inputs.ProfileStatus) &&
		manifest.Settings.OfferedSendQPS > 0 && manifest.Settings.RequiredActiveConnections > 0 &&
		manifest.Settings.ConfiguredGroupMembers > 1 &&
		manifest.Settings.ConfiguredWarmupSeconds > 0 && manifest.Settings.ConfiguredMeasuredSeconds > 0 &&
		manifest.Settings.ConfiguredDrainBudgetSeconds > 0 && manifest.Settings.MaximumSampleGapSeconds > 0 &&
		validRelativeArtifactPath(manifest.Evidence) && validLowerDigest(manifest.EvidenceSHA256) &&
		validRelativeArtifactPath(manifest.Result) && validLowerDigest(manifest.ResultSHA256)
}

// ValidateStepClosure replays the deterministic step decision and rejects an
// unbound or caller-authored result. Filesystem and raw-payload validation are
// deliberately owned by the command-side closure verifier.
func ValidateStepClosure(closure StepClosure) bool {
	if closure.Schema != StepClosureSchema || !validRelativeArtifactPath(closure.ClosureManifest) ||
		!validLowerDigest(closure.ClosureManifestSHA256) || !validLowerDigest(closure.Result.PayloadManifestSHA256) {
		return false
	}
	want := CloseStepResult(closure.Evidence, closure.Result.PayloadManifestSHA256)
	return reflect.DeepEqual(want, closure.Result)
}

// SealBaselineEvidence binds one baseline decision envelope to its exact
// ordered step closures. It is used for both terminal preflight decisions and
// measured baselines; a preflight envelope remains unauthorized because it has
// no reviewed closures.
func SealBaselineEvidence(evidence *BaselineEvidence) {
	if evidence == nil {
		return
	}
	evidence.CompletionGeneration = baselineCompletionGeneration(*evidence)
}

func baselineCompletionGeneration(evidence BaselineEvidence) string {
	evidence.CompletionGeneration = ""
	data, err := json.Marshal(evidence)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func validRelativeArtifactPath(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return false
	}
	parts := strings.Split(value, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func validLowerDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
