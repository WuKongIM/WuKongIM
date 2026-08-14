package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/bench/localbaseline"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

const localSingleNodeCompletionSchema = "wukongim/chat-lifecycle-local-single-node-baseline/v1"

type localSingleNodeCompletionMarker struct {
	Schema                        string `json:"schema"`
	CompletionMarker              bool   `json:"completion_marker"`
	CompletionGeneration          string `json:"completion_generation"`
	BaselineInvocationID          string `json:"baseline_invocation_id"`
	ArtifactManifestSHA256        string `json:"artifact_manifest_sha256"`
	TypedAuthorizationSHA256      string `json:"typed_authorization_sha256"`
	Outcome                       string `json:"outcome"`
	Reason                        string `json:"reason"`
	ReviewedContract              bool   `json:"reviewed_contract"`
	ReviewedContractSatisfied     bool   `json:"reviewed_contract_satisfied"`
	ReviewedTypedEvidenceComplete bool   `json:"reviewed_typed_lifecycle_evidence_complete"`
	OnlineConnections             int    `json:"online_connections"`
	HighestCleanRate              int    `json:"highest_clean_rate"`
	FirstFailingRate              int    `json:"first_failing_rate"`
	AuthorizesThreeNodeDiagnostic bool   `json:"authorizes_three_node_diagnostic"`
	QPSList                       string `json:"qps_list"`
	LogicalSlotGroups             int    `json:"logical_slot_groups"`
	HashSlots                     int    `json:"hash_slots"`
	SlotReplicas                  int    `json:"slot_replicas"`
	ChannelReplicas               int    `json:"channel_replicas"`
	CommitCoordinatorFlushWindow  string `json:"commit_coordinator_flush_window"`
	CommitCoordinatorShards       int    `json:"commit_coordinator_shards"`
	SyncCommit                    bool   `json:"sync_commit"`
	MinimumFilesystemFreePercent  int    `json:"minimum_filesystem_free_percent"`
	FilesystemObservationComplete *bool  `json:"filesystem_observation_complete"`
	ObservedFilesystemFreePercent int    `json:"observed_filesystem_free_percent"`
	CanonicalDataDir              string `json:"canonical_data_dir"`
	DataFilesystemDevice          string `json:"data_filesystem_device"`
	DataFilesystemTotalBlocks     uint64 `json:"data_filesystem_total_blocks"`
	DataFilesystemBlockSize       uint64 `json:"data_filesystem_block_size"`
	SourceRevision                string `json:"source_revision"`
	SourceDirty                   bool   `json:"source_dirty"`
	SourceSealValid               bool   `json:"source_seal_valid"`
	ArtifactSealValid             bool   `json:"artifact_seal_valid"`
	ArtifactIdentity              string `json:"artifact_identity"`
	TypedEvidence                 string `json:"typed_evidence"`
	TypedAuthorization            string `json:"typed_authorization"`
	EffectiveConfig               string `json:"effective_config"`
	Summary                       string `json:"summary"`
	StorageSummary                string `json:"storage_summary"`
	HostIOSummary                 string `json:"host_io_summary"`
	ArtifactChecksums             string `json:"artifact_checksums"`
}

func newLocalSingleNodeCompletionCommand() *cobra.Command {
	var rootPath, markerPath string
	cmd := &cobra.Command{
		Use:   "local-single-node-completion",
		Short: "Verify the atomic single-node cluster completion marker",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			result, err := verifyLocalSingleNodeCompletion(rootPath, markerPath)
			if err != nil {
				return commandExit{code: exitInternal, message: "local single-node completion verification failed: " + err.Error()}
			}
			return exitCodeError(localSingleNodeAuthorizationExitCode(result))
		},
	}
	cmd.Flags().StringVar(&rootPath, "root", "", "sealed single-node cluster evidence root")
	cmd.Flags().StringVar(&markerPath, "marker", "", "atomically published local-baseline.json marker")
	for _, name := range []string{"root", "marker"} {
		if err := cmd.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	return cmd
}

func verifyLocalSingleNodeCompletion(rootPath, markerPath string) (localbaseline.AuthorizationResult, error) {
	artifactRoot, err := openLocalSingleNodeArtifactRoot(rootPath)
	if err != nil {
		return localbaseline.AuthorizationResult{}, err
	}
	markerRelative, err := artifactRoot.relative(markerPath)
	if err != nil {
		return localbaseline.AuthorizationResult{}, err
	}
	if markerRelative != "local-baseline.json" {
		return localbaseline.AuthorizationResult{}, fmt.Errorf("completion marker must be root/local-baseline.json")
	}
	data, err := artifactRoot.read(markerRelative, 1<<20)
	if err != nil {
		return localbaseline.AuthorizationResult{}, err
	}
	return verifyLocalSingleNodeCompletionData(artifactRoot, data)
}

func verifyLocalSingleNodeCompletionData(artifactRoot localSingleNodeArtifactRoot, data []byte) (localbaseline.AuthorizationResult, error) {
	var completion localSingleNodeCompletionMarker
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&completion); err != nil {
		return localbaseline.AuthorizationResult{}, fmt.Errorf("decode completion marker: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return localbaseline.AuthorizationResult{}, fmt.Errorf("decode completion marker: trailing data")
	}
	if completion.Schema != localSingleNodeCompletionSchema || !completion.CompletionMarker ||
		!validLocalSingleNodeInvocationID(completion.BaselineInvocationID) ||
		!validLocalSingleNodeDigest(completion.CompletionGeneration) ||
		!validLocalSingleNodeDigest(completion.ArtifactManifestSHA256) ||
		!validLocalSingleNodeDigest(completion.TypedAuthorizationSHA256) {
		return localbaseline.AuthorizationResult{}, fmt.Errorf("completion marker identity is invalid")
	}
	if completion.ArtifactChecksums != "checksums.sha256" || completion.TypedEvidence != "reports/local-baseline-evidence.json" ||
		completion.TypedAuthorization != "reports/local-baseline-authorization.json" ||
		completion.ArtifactIdentity != "artifact-identity.tsv" || completion.EffectiveConfig != "config/effective-wukongim.toml" ||
		completion.Summary != "summary.tsv" || completion.StorageSummary != "storage_metrics_summary.tsv" ||
		completion.HostIOSummary != "host_io_summary.tsv" {
		return localbaseline.AuthorizationResult{}, fmt.Errorf("completion marker paths are invalid")
	}
	manifest, err := verifyLocalSingleNodeChecksumManifestAtRoot(artifactRoot, filepath.Join(artifactRoot.requestedAbs, completion.ArtifactChecksums))
	if err != nil {
		return localbaseline.AuthorizationResult{}, err
	}
	if manifest.digest != completion.ArtifactManifestSHA256 {
		return localbaseline.AuthorizationResult{}, fmt.Errorf("completion marker manifest digest mismatch")
	}
	if _, included := manifest.entries["local-baseline.json"]; included {
		return localbaseline.AuthorizationResult{}, fmt.Errorf("completion marker must be published after and outside its manifest")
	}
	for label, relative := range map[string]string{
		"typed evidence": completion.TypedEvidence, "typed authorization": completion.TypedAuthorization,
		"artifact identity": completion.ArtifactIdentity, "effective config": completion.EffectiveConfig,
	} {
		if _, err := manifest.bytesForRelative(relative); err != nil {
			return localbaseline.AuthorizationResult{}, fmt.Errorf("%s: %w", label, err)
		}
	}
	authorizationData, _ := manifest.bytesForRelative(completion.TypedAuthorization)
	if digestLocalSingleNodeBytes(authorizationData) != completion.TypedAuthorizationSHA256 {
		return localbaseline.AuthorizationResult{}, fmt.Errorf("typed authorization digest mismatch")
	}
	var authorization localbaseline.AuthorizationResult
	decoder = json.NewDecoder(bytes.NewReader(authorizationData))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&authorization); err != nil {
		return localbaseline.AuthorizationResult{}, fmt.Errorf("decode typed authorization: %w", err)
	}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return localbaseline.AuthorizationResult{}, fmt.Errorf("decode typed authorization: trailing data")
	}
	evidenceData, _ := manifest.bytesForRelative(completion.TypedEvidence)
	evidence, err := localbaseline.ParseBaselineEvidence(bytes.NewReader(evidenceData))
	if err != nil {
		return localbaseline.AuthorizationResult{}, fmt.Errorf("decode typed evidence: %w", err)
	}
	identityData, _ := manifest.bytesForRelative(completion.ArtifactIdentity)
	configData, _ := manifest.bytesForRelative(completion.EffectiveConfig)
	identity, err := parseLocalSingleNodeArtifactIdentity(identityData)
	if err != nil {
		return localbaseline.AuthorizationResult{}, err
	}
	measured := identity["seal_scope"] == "measured"
	if !measured && len(evidence.StepClosures) != 0 {
		return localbaseline.AuthorizationResult{}, fmt.Errorf("preflight completion must not contain measured step closures")
	}
	verifiedClosures := make([]localbaseline.StepClosure, 0, len(evidence.StepClosures))
	for _, claimed := range evidence.StepClosures {
		closure, closureErr := verifyLocalSingleNodeStepClosureFromManifest(manifest, claimed.ClosureManifest)
		if closureErr != nil {
			return localbaseline.AuthorizationResult{}, fmt.Errorf("step closure %s: %w", claimed.ClosureManifest, closureErr)
		}
		if !reflect.DeepEqual(claimed, closure) {
			return localbaseline.AuthorizationResult{}, fmt.Errorf("typed evidence closure does not match sealed closure")
		}
		verifiedClosures = append(verifiedClosures, closure)
	}
	evidence.StepClosures = verifiedClosures
	if measured {
		summaryData, summaryErr := manifest.bytesForRelative(completion.Summary)
		storageSummaryData, storageSummaryErr := manifest.bytesForRelative(completion.StorageSummary)
		hostSummaryData, hostSummaryErr := manifest.bytesForRelative(completion.HostIOSummary)
		if summaryErr != nil || storageSummaryErr != nil || hostSummaryErr != nil {
			return localbaseline.AuthorizationResult{}, fmt.Errorf("measured summary artifacts changed during verification")
		}
		if err := validateLocalSingleNodeSummaryArtifacts(summaryData, storageSummaryData, hostSummaryData, verifiedClosures); err != nil {
			return localbaseline.AuthorizationResult{}, err
		}
	}
	recomputed := localbaseline.AuthorizeThreeNodeDiagnostic(evidence)
	if !reflect.DeepEqual(recomputed, authorization) {
		return localbaseline.AuthorizationResult{}, fmt.Errorf("typed authorization does not match recomputed authorization")
	}
	if err := validateLocalSingleNodeFilesystemDecision(evidence, authorization); err != nil {
		return localbaseline.AuthorizationResult{}, err
	}
	if identity["effective_config"] != completion.EffectiveConfig ||
		identity["effective_config_sha256"] != digestLocalSingleNodeBytes(configData) ||
		identity["source_revision"] != evidence.Source.Revision ||
		identity["source_revision"] != completion.SourceRevision ||
		identity["source_dirty"] != strconv.FormatBool(evidence.Source.Dirty) ||
		completion.SourceDirty != evidence.Source.Dirty ||
		identity["source_rebuildable_from_revision"] != strconv.FormatBool(evidence.Source.RebuildableFromRevision) ||
		identity["baseline_invocation_id"] != evidence.BaselineInvocationID ||
		identity["baseline_invocation_id"] != completion.BaselineInvocationID ||
		identity["canonical_data_dir"] != evidence.CanonicalDataDir ||
		identity["data_filesystem_device"] != evidence.DataFilesystemDevice ||
		identity["data_filesystem_total_blocks"] != strconv.FormatUint(evidence.DataFilesystemTotalBlocks, 10) ||
		identity["data_filesystem_block_size"] != strconv.FormatUint(evidence.DataFilesystemBlockSize, 10) {
		return localbaseline.AuthorizationResult{}, fmt.Errorf("artifact identity does not match typed evidence")
	}
	if measured {
		if len(verifiedClosures) != len(localbaseline.ReviewedOfferedSendQPS) {
			return localbaseline.AuthorizationResult{}, fmt.Errorf("measured completion has no complete step execution seal")
		}
		stepSeal := verifiedClosures[0].Evidence.ExecutionSeal
		if stepSeal.BaselineInvocationID != identity["baseline_invocation_id"] ||
			stepSeal.SourceConfigSHA256 != identity["original_config_sha256"] ||
			stepSeal.EffectiveConfigSHA256 != identity["effective_config_sha256"] ||
			stepSeal.WukongIMBinarySHA256 != identity["wukongim_binary_sha256"] ||
			stepSeal.WkbenchBinarySHA256 != identity["wkbench_binary_sha256"] {
			return localbaseline.AuthorizationResult{}, fmt.Errorf("step execution seal source config or binaries do not match global artifact identity")
		}
		for label, keys := range map[string][2]string{
			"wukongim binary": {"wukongim_binary", "wukongim_binary_sha256"},
			"wkbench binary":  {"wkbench_binary", "wkbench_binary_sha256"},
		} {
			if requireErr := manifest.verifyCurrentDigest(identity[keys[0]], identity[keys[1]]); requireErr != nil {
				return localbaseline.AuthorizationResult{}, fmt.Errorf("%s identity is not sealed", label)
			}
		}
	} else {
		if requireErr := manifest.verifyCurrentDigest(identity["wkbench_binary"], identity["wkbench_binary_sha256"]); requireErr != nil {
			return localbaseline.AuthorizationResult{}, fmt.Errorf("preflight verifier identity is not sealed")
		}
	}
	var reviewedTarget localbaseline.ReviewedTargetEvidence
	if len(verifiedClosures) > 0 {
		reviewedTarget = verifiedClosures[0].Evidence.Target
	}
	if err := validateLocalSingleNodeEffectiveConfig(configData, evidence.Settings, reviewedTarget, measured); err != nil {
		return localbaseline.AuthorizationResult{}, err
	}
	sourceSeal := !evidence.Source.Dirty && evidence.Source.RebuildableFromRevision
	typedComplete := evidence.Seal.PayloadComplete && evidence.Seal.ChecksumsVerified &&
		len(evidence.StepClosures) == len(localbaseline.ReviewedOfferedSendQPS)
	qpsListValid := completion.QPSList == "250,500,750,1000"
	if !measured {
		qpsListValid = validLocalSingleNodePreflightQPSList(completion.QPSList)
	}
	if !completion.ArtifactSealValid || completion.SourceSealValid != sourceSeal ||
		completion.ReviewedTypedEvidenceComplete != typedComplete ||
		completion.ReviewedContract != authorization.ReviewedContractSatisfied ||
		completion.OnlineConnections != evidence.Settings.ActiveConnections || !qpsListValid ||
		completion.LogicalSlotGroups != evidence.Settings.LogicalSlotGroups ||
		completion.HashSlots != evidence.Settings.HashSlots || completion.SlotReplicas != evidence.Settings.SlotReplicas ||
		completion.ChannelReplicas != evidence.Settings.ChannelReplicas ||
		completion.CommitCoordinatorFlushWindow != fmt.Sprintf("%dus", evidence.Settings.CommitFlushWindowMicros) ||
		completion.CommitCoordinatorShards != evidence.Settings.CommitCoordinatorShards ||
		completion.SyncCommit != evidence.Settings.SyncCommit ||
		completion.MinimumFilesystemFreePercent != evidence.Settings.MinimumFreePercent ||
		completion.FilesystemObservationComplete == nil ||
		*completion.FilesystemObservationComplete != evidence.FilesystemObservationComplete ||
		completion.ObservedFilesystemFreePercent != evidence.ObservedFilesystemFreePercent ||
		completion.CanonicalDataDir != evidence.CanonicalDataDir ||
		completion.DataFilesystemDevice != evidence.DataFilesystemDevice ||
		completion.DataFilesystemTotalBlocks != evidence.DataFilesystemTotalBlocks ||
		completion.DataFilesystemBlockSize != evidence.DataFilesystemBlockSize {
		return localbaseline.AuthorizationResult{}, fmt.Errorf("completion marker does not match typed evidence and configuration")
	}
	if authorization.Schema != localbaseline.AuthorizationResultSchema ||
		authorization.CompletionGeneration != completion.CompletionGeneration ||
		string(authorization.Outcome) != completion.Outcome || authorization.Reason != completion.Reason ||
		authorization.HighestCleanRate != completion.HighestCleanRate ||
		authorization.FirstFailingRate != completion.FirstFailingRate ||
		completion.ReviewedContract != completion.ReviewedContractSatisfied ||
		authorization.ReviewedContractSatisfied != completion.ReviewedContractSatisfied ||
		authorization.Authorizes != completion.AuthorizesThreeNodeDiagnostic {
		return localbaseline.AuthorizationResult{}, fmt.Errorf("completion marker does not match typed authorization")
	}
	if completion.AuthorizesThreeNodeDiagnostic &&
		(!completion.ArtifactSealValid || !completion.SourceSealValid || !completion.ReviewedTypedEvidenceComplete ||
			completion.Outcome != string(localbaseline.OutcomeClean)) {
		return localbaseline.AuthorizationResult{}, fmt.Errorf("completion marker exposes an unsealed authorization")
	}
	return authorization, nil
}

var localSingleNodeSummaryHeader = []string{
	"tag", "offered_qps", "status", "exit_status", "actual_qps", "send_success", "send_errors",
	"connect_error_rate", "sendack_error_rate", "p50_seconds", "p95_seconds", "p99_seconds", "max_seconds",
	"connect_success", "scheduler_planned", "scheduler_dispatched", "scheduler_dropped",
}

func validateLocalSingleNodeSummaryArtifacts(
	summaryData, storageData, hostData []byte,
	closures []localbaseline.StepClosure,
) error {
	rows, err := parseLocalSingleNodeSummaryRows(summaryData)
	if err != nil {
		return err
	}
	storageRows, err := localbaseline.ParseStorageMetricsSummaryRows(bytes.NewReader(storageData))
	if err != nil {
		return fmt.Errorf("storage summary content: %w", err)
	}
	hostRows, err := localbaseline.ParseHostIOSummaryRows(bytes.NewReader(hostData))
	if err != nil {
		return fmt.Errorf("host I/O summary content: %w", err)
	}
	if len(rows) != len(closures) || len(storageRows) != len(closures) || len(hostRows) != len(closures) {
		return fmt.Errorf("summary row counts do not match typed step closures")
	}
	for index, closure := range closures {
		if err := validateLocalSingleNodeSummaryRow(rows[index], closure); err != nil {
			return fmt.Errorf("summary row %d: %w", index+1, err)
		}
		if !reflect.DeepEqual(storageRows[index], closure.Evidence.StorageMetrics) {
			return fmt.Errorf("storage summary row %d does not match typed step evidence", index+1)
		}
		if !reflect.DeepEqual(hostRows[index], closure.Evidence.HostIO) {
			return fmt.Errorf("host I/O summary row %d does not match typed step evidence", index+1)
		}
	}
	return nil
}

func parseLocalSingleNodeSummaryRows(data []byte) ([][]string, error) {
	if len(data) == 0 || len(data) > localbaseline.MaximumSummaryEvidenceBytes ||
		bytes.IndexByte(data, '\r') >= 0 || data[len(data)-1] != '\n' {
		return nil, fmt.Errorf("summary content framing is invalid")
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	if !scanner.Scan() || scanner.Text() != strings.Join(localSingleNodeSummaryHeader, "\t") {
		return nil, fmt.Errorf("summary content schema is invalid")
	}
	rows := make([][]string, 0, len(localbaseline.ReviewedOfferedSendQPS))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != len(localSingleNodeSummaryHeader) {
			return nil, fmt.Errorf("summary content row has %d fields, want %d", len(fields), len(localSingleNodeSummaryHeader))
		}
		for _, field := range fields {
			if field == "" || strings.TrimSpace(field) != field {
				return nil, fmt.Errorf("summary content row has an empty or padded field")
			}
		}
		rows = append(rows, fields)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("summary content: %w", err)
	}
	return rows, nil
}

func validateLocalSingleNodeSummaryRow(fields []string, closure localbaseline.StepClosure) error {
	evidence := closure.Evidence
	if fields[0] != fmt.Sprintf("%06d", evidence.OfferedSendQPS) {
		return fmt.Errorf("tag does not match typed step")
	}
	offered, err := strconv.Atoi(fields[1])
	if err != nil || offered != evidence.OfferedSendQPS {
		return fmt.Errorf("offered QPS does not match typed step")
	}
	if fields[2] != "passed" && fields[2] != "failed" && fields[2] != "missing_report" {
		return fmt.Errorf("status is invalid")
	}
	if closure.Result.Clean && fields[2] != "passed" {
		return fmt.Errorf("clean typed step does not have a passed raw summary")
	}
	exitStatus, err := strconv.ParseUint(fields[3], 10, 8)
	if err != nil || (closure.Result.Clean && exitStatus != 0) {
		return fmt.Errorf("exit status contradicts typed step")
	}
	actualQPS, err := parseLocalSingleNodeNonnegativeFloat(fields[4])
	if err != nil {
		return fmt.Errorf("actual QPS is invalid")
	}
	wantActualQPS := float64(evidence.Traffic.SendACKs) / float64(evidence.ConfiguredMeasuredSeconds)
	if math.Abs(actualQPS-wantActualQPS) > 0.000001 {
		return fmt.Errorf("actual QPS does not match typed traffic")
	}
	uintFields := make(map[int]uint64, 7)
	for _, index := range []int{5, 6, 13, 14, 15, 16} {
		value, parseErr := strconv.ParseUint(fields[index], 10, 64)
		if parseErr != nil {
			return fmt.Errorf("field %q is not an unsigned integer", localSingleNodeSummaryHeader[index])
		}
		uintFields[index] = value
	}
	if uintFields[5] != evidence.Traffic.SendACKs || uintFields[6] != evidence.Traffic.TerminalErrors ||
		uintFields[13] != uint64(evidence.RequiredActiveConnections) || uintFields[14] != evidence.Traffic.Planned ||
		uintFields[15] != evidence.Traffic.Dispatched || uintFields[16] != evidence.Traffic.Planned-evidence.Traffic.Dispatched {
		return fmt.Errorf("raw counters do not match typed traffic")
	}
	floats := make(map[int]float64, 6)
	for _, index := range []int{7, 8, 9, 10, 11, 12} {
		value, parseErr := parseLocalSingleNodeNonnegativeFloat(fields[index])
		if parseErr != nil {
			return fmt.Errorf("field %q is invalid", localSingleNodeSummaryHeader[index])
		}
		floats[index] = value
	}
	if floats[7] > 1 || floats[8] > 1 || floats[9] > floats[10] || floats[10] > floats[11] || floats[11] > floats[12] {
		return fmt.Errorf("raw error rates or latency distribution are inconsistent")
	}
	return nil
}

func parseLocalSingleNodeNonnegativeFloat(value string) (float64, error) {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < 0 {
		return 0, fmt.Errorf("not a finite non-negative number")
	}
	return parsed, nil
}

func validateLocalSingleNodeFilesystemDecision(
	evidence localbaseline.BaselineEvidence,
	authorization localbaseline.AuthorizationResult,
) error {
	if evidence.ObservedFilesystemFreePercent < 0 || evidence.ObservedFilesystemFreePercent > 100 {
		return fmt.Errorf("typed filesystem observation percent is invalid")
	}
	if !evidence.FilesystemObservationComplete {
		if authorization.Authorizes || authorization.Outcome != localbaseline.OutcomeInsufficientEvidence ||
			authorization.ExitCode != exitInternal || !validLocalSingleNodeFilesystemIncompleteReason(authorization.Reason) {
			return fmt.Errorf("incomplete filesystem observation has a contradictory authorization")
		}
		return nil
	}
	if evidence.ObservedFilesystemFreePercent < evidence.Settings.MinimumFreePercent {
		if localbaseline.Outcome(evidence.DiagnosticOutcome) == localbaseline.OutcomeInsufficientEvidence {
			if authorization.Authorizes || authorization.Outcome != localbaseline.OutcomeInsufficientEvidence ||
				authorization.ExitCode != exitInternal {
				return fmt.Errorf("low filesystem observation has a contradictory evidence-failure authorization")
			}
		} else if authorization.Authorizes || authorization.Outcome != localbaseline.OutcomeStorageConfounded ||
			authorization.ExitCode != exitPreflight {
			return fmt.Errorf("low filesystem observation has a contradictory authorization")
		}
	}
	return nil
}

func validLocalSingleNodeFilesystemIncompleteReason(reason string) bool {
	switch reason {
	case string(localbaseline.AuthorizationReasonFilesystem), "filesystem_preflight_unavailable", "filesystem_observation_missing":
		return true
	default:
		return false
	}
}

func parseLocalSingleNodeArtifactIdentity(data []byte) (map[string]string, error) {
	if len(data) == 0 || len(data) > 64<<10 {
		return nil, fmt.Errorf("artifact identity is empty or oversized")
	}
	values := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), "\t")
		if len(parts) != 2 || strings.TrimSpace(parts[0]) != parts[0] || strings.TrimSpace(parts[1]) != parts[1] || parts[0] == "" {
			return nil, fmt.Errorf("artifact identity row is invalid")
		}
		if _, duplicate := values[parts[0]]; duplicate {
			return nil, fmt.Errorf("artifact identity key is duplicated")
		}
		values[parts[0]] = parts[1]
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if values["schema"] != "wukongim/chat-lifecycle-local-single-node-artifact-identity/v1" {
		return nil, fmt.Errorf("artifact identity schema is invalid")
	}
	required := []string{
		"schema", "baseline_invocation_id", "source_revision", "source_dirty", "source_rebuildable_from_revision", "source_capture", "seal_scope",
		"canonical_data_dir", "data_filesystem_device", "data_filesystem_total_blocks", "data_filesystem_block_size",
		"original_config_sha256", "effective_config", "effective_config_sha256", "wukongim_binary", "wukongim_binary_sha256",
		"wkbench_binary", "wkbench_binary_sha256",
	}
	if len(values) != len(required) {
		return nil, fmt.Errorf("artifact identity field set is invalid")
	}
	for _, key := range required {
		if _, ok := values[key]; !ok {
			return nil, fmt.Errorf("artifact identity field %q is absent", key)
		}
	}
	if values["source_dirty"] != "true" && values["source_dirty"] != "false" {
		return nil, fmt.Errorf("artifact identity source dirty value is invalid")
	}
	if !validLocalSingleNodeInvocationID(values["baseline_invocation_id"]) {
		return nil, fmt.Errorf("artifact identity baseline invocation is invalid")
	}
	if values["source_rebuildable_from_revision"] != "true" && values["source_rebuildable_from_revision"] != "false" {
		return nil, fmt.Errorf("artifact identity source rebuildability is invalid")
	}
	if !filepath.IsAbs(values["canonical_data_dir"]) || filepath.Clean(values["canonical_data_dir"]) != values["canonical_data_dir"] ||
		values["data_filesystem_device"] == "" || strings.TrimSpace(values["data_filesystem_device"]) != values["data_filesystem_device"] {
		return nil, fmt.Errorf("artifact data filesystem identity is invalid")
	}
	totalBlocks, totalErr := strconv.ParseUint(values["data_filesystem_total_blocks"], 10, 64)
	blockSize, blockErr := strconv.ParseUint(values["data_filesystem_block_size"], 10, 64)
	if totalErr != nil || blockErr != nil ||
		(values["data_filesystem_device"] == "unavailable" && (totalBlocks != 0 || blockSize != 0)) ||
		(values["data_filesystem_device"] != "unavailable" && (totalBlocks == 0 || blockSize == 0)) {
		return nil, fmt.Errorf("artifact data filesystem geometry is invalid")
	}
	if !validLocalSingleNodeDigest(values["original_config_sha256"]) ||
		values["effective_config"] != "config/effective-wukongim.toml" || !validLocalSingleNodeDigest(values["effective_config_sha256"]) ||
		values["wukongim_binary"] != "bin/wukongim" || values["wkbench_binary"] != "bin/wkbench" {
		return nil, fmt.Errorf("artifact identity paths or effective config digest are invalid")
	}
	switch values["seal_scope"] {
	case "measured":
		if values["source_capture"] != "revision_and_binary_identity" ||
			!validLocalSingleNodeDigest(values["wukongim_binary_sha256"]) || !validLocalSingleNodeDigest(values["wkbench_binary_sha256"]) {
			return nil, fmt.Errorf("artifact identity is not a measured source seal")
		}
	case "preflight":
		if values["source_capture"] != "binary_identity_only" || values["wukongim_binary_sha256"] != "unavailable" ||
			!validLocalSingleNodeDigest(values["wkbench_binary_sha256"]) {
			return nil, fmt.Errorf("artifact identity is not a preflight source seal")
		}
	default:
		return nil, fmt.Errorf("artifact identity seal scope is invalid")
	}
	return values, nil
}

func validateLocalSingleNodeEffectiveConfig(
	data []byte,
	settings localbaseline.ReviewedSettings,
	target localbaseline.ReviewedTargetEvidence,
	requireReviewedTopology bool,
) error {
	if len(data) > 4<<20 || (requireReviewedTopology && len(data) == 0) {
		return fmt.Errorf("effective config is empty or oversized")
	}
	if len(data) == 0 {
		return nil
	}
	var document struct {
		Node struct {
			ID uint64 `toml:"id"`
		} `toml:"node"`
		Cluster struct {
			ListenAddr string `toml:"listen_addr"`
			Nodes      []struct {
				ID   uint64 `toml:"id"`
				Addr string `toml:"addr"`
			} `toml:"nodes"`
			Seeds []string `toml:"seeds"`
		} `toml:"cluster"`
		API struct {
			ListenAddr      string `toml:"listen_addr"`
			ExternalTCPAddr string `toml:"external_tcp_addr"`
		} `toml:"api"`
		Gateway struct {
			Listeners []struct {
				Address   string `toml:"address"`
				Network   string `toml:"network"`
				Protocol  string `toml:"protocol"`
				Transport string `toml:"transport"`
			} `toml:"listeners"`
		} `toml:"gateway"`
		Bench struct {
			APIEnable bool `toml:"api_enable"`
		} `toml:"bench"`
		Observability struct {
			MetricsEnable bool `toml:"metrics_enable"`
		} `toml:"observability"`
		Runtime struct {
			TopologyEnvironmentOverridesRejected bool   `toml:"topology_environment_overrides_rejected"`
			EndpointEnvironmentOverridesRejected bool   `toml:"endpoint_environment_overrides_rejected"`
			InitialSlotCount                     int    `toml:"initial_slot_count"`
			HashSlotCount                        int    `toml:"hash_slot_count"`
			SlotReplicaN                         int    `toml:"slot_replica_n"`
			ChannelReplicaN                      int    `toml:"channel_replica_n"`
			CommitCoordinatorFlushWindow         string `toml:"commit_coordinator_flush_window"`
			CommitCoordinatorShards              int    `toml:"commit_coordinator_shards"`
			CommitCoordinatorSync                bool   `toml:"commit_coordinator_sync"`
		} `toml:"local_single_node_runtime"`
	}
	if err := toml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("effective config parse failed: %w", err)
	}
	if !requireReviewedTopology {
		return nil
	}
	if document.Node.ID == 0 || len(document.Cluster.Nodes) != 1 ||
		document.Cluster.Nodes[0].ID != document.Node.ID || strings.TrimSpace(document.Cluster.Nodes[0].Addr) == "" ||
		len(document.Cluster.Seeds) != 0 {
		return fmt.Errorf("effective config is not an exact single-node cluster topology")
	}
	clusterListen, clusterListenOK := canonicalReviewedLoopbackTCPAddress(document.Cluster.ListenAddr)
	clusterNode, clusterNodeOK := canonicalReviewedLoopbackTCPAddress(document.Cluster.Nodes[0].Addr)
	apiListen, apiListenOK := canonicalReviewedLoopbackTCPAddress(document.API.ListenAddr)
	gatewayExternal, gatewayExternalOK := canonicalReviewedLoopbackTCPAddress(document.API.ExternalTCPAddr)
	gatewayListen, gatewayListenOK := reviewedWKProtoGatewayListener(document.Gateway.Listeners)
	if !clusterListenOK || !clusterNodeOK || clusterListen != clusterNode ||
		!apiListenOK || !gatewayExternalOK || !gatewayListenOK || gatewayExternal != gatewayListen ||
		!document.Bench.APIEnable || !document.Observability.MetricsEnable ||
		target.APIAddress != "http://"+apiListen || target.MetricsAddress != target.APIAddress ||
		target.GatewayAddress != gatewayListen {
		return fmt.Errorf("effective config listeners do not match the sealed local execution target")
	}
	runtime := document.Runtime
	if !runtime.TopologyEnvironmentOverridesRejected || !runtime.EndpointEnvironmentOverridesRejected ||
		runtime.InitialSlotCount != settings.LogicalSlotGroups || runtime.HashSlotCount != settings.HashSlots ||
		runtime.SlotReplicaN != settings.SlotReplicas || runtime.ChannelReplicaN != settings.ChannelReplicas ||
		runtime.CommitCoordinatorFlushWindow != fmt.Sprintf("%dus", settings.CommitFlushWindowMicros) ||
		runtime.CommitCoordinatorShards != settings.CommitCoordinatorShards || runtime.CommitCoordinatorSync != settings.SyncCommit {
		return fmt.Errorf("effective config does not match typed reviewed settings")
	}
	return nil
}

func reviewedWKProtoGatewayListener(listeners []struct {
	Address   string `toml:"address"`
	Network   string `toml:"network"`
	Protocol  string `toml:"protocol"`
	Transport string `toml:"transport"`
}) (string, bool) {
	var address string
	matches := 0
	for _, listener := range listeners {
		if strings.EqualFold(strings.TrimSpace(listener.Network), "tcp") &&
			strings.EqualFold(strings.TrimSpace(listener.Protocol), "wkproto") {
			matches++
			if !strings.EqualFold(strings.TrimSpace(listener.Transport), "gnet") {
				return "", false
			}
			canonical, ok := canonicalReviewedLoopbackTCPAddress(listener.Address)
			if !ok {
				return "", false
			}
			address = canonical
		}
	}
	return address, matches == 1
}

func validLocalSingleNodeDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func validLocalSingleNodeInvocationID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validLocalSingleNodePreflightQPSList(value string) bool {
	parts := strings.Split(value, ",")
	if len(parts) == 0 || len(parts) > 16 {
		return false
	}
	for _, part := range parts {
		qps, err := strconv.Atoi(part)
		if err != nil || qps <= 0 || strconv.Itoa(qps) != part {
			return false
		}
	}
	return true
}

func digestLocalSingleNodeBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
