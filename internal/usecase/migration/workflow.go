package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// Plan binds offline inputs and the target generation before any source scan.
type Plan struct {
	Version int `json:"version"`
	// SourceCommit pins the reader schema revision, not an identified source binary.
	// ReadPlan normalizes an omitted or empty value before hashing or archiving.
	SourceCommit string        `json:"source_commit"`
	Sources      []NodeOptions `json:"sources"`
	Target       TargetPlan    `json:"target"`
	// Exclusions records operator-authorized omissions from the target. Raw
	// source rows remain in the checksummed archive; nil preserves all business.
	Exclusions *Exclusions `json:"exclusions,omitempty"`
	// Messages opts into explicitly authorized omissions and sequence mapping.
	Messages *MessagePolicy `json:"messages,omitempty"`
	// Quarantine binds exact malformed primary rows to an operator decision.
	// Original bytes and dependent indexes remain in the source archive.
	Quarantine []QuarantineRow `json:"quarantine,omitempty"`
	// Metadata binds explicitly chosen original metadata lookup semantics.
	Metadata *MetadataPolicy `json:"metadata,omitempty"`
	// History binds evidence-qualified replica lag and explicit recovery. Nil keeps
	// the original requirement that all formal message replicas agree.
	History *HistoryPolicy `json:"history,omitempty"`
	// PluginNodes explicitly chooses the source settings for every target;
	// one source can supply several targets when changing the cluster size.
	// It does not authorize a plugin business compatibility mapping.
	PluginNodes []PluginNodeMapping `json:"plugin_nodes,omitempty"`
	// PluginConfigs selects one captured node's effective config for a named
	// plugin on every mapped target. Original per-node state remains archived.
	PluginConfigs []PluginConfigMapping `json:"plugin_configs,omitempty"`
	// PluginArtifacts captures exact original executables without declaring
	// their business behavior compatible. Every source copy remains archived.
	PluginArtifacts []PluginArtifactSpec `json:"plugin_artifacts,omitempty"`
}

// ReadPlan applies the supported reader revision before binding migration identity.
// Explicit revision mismatches still fail; data compatibility is checked during capture.
func ReadPlan(reader io.Reader, sourceCommit string) (plan Plan, err error) {
	d := json.NewDecoder(io.LimitReader(reader, (1<<20)+1))
	d.DisallowUnknownFields()
	if err := d.Decode(&plan); err != nil {
		return plan, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return plan, errors.New("trailing migration plan data")
	}
	if plan.Version != 1 {
		return plan, errors.New("unsupported migration plan version")
	}
	if plan.SourceCommit == "" {
		plan.SourceCommit = sourceCommit
	}
	if plan.SourceCommit != sourceCommit {
		return plan, errors.New("unsupported source commit; this tool only supports its pinned v2 schema revision")
	}
	if len(plan.Sources) == 0 || len(plan.Sources) > 1024 {
		return plan, errors.New("migration plan requires complete source node directories")
	}
	for _, source := range plan.Sources {
		if source.NodeID == 0 || source.ShardCount < 1 || source.ShardCount > 1024 || !filepath.IsAbs(source.DataDir) {
			return plan, errors.New("invalid source node identity, shard count or absolute data directory")
		}
	}
	if err := validateQuarantinePolicy(plan); err != nil {
		return plan, err
	}
	if err := validateMessagePolicy(plan.Messages); err != nil {
		return plan, err
	}
	if err := validateHistoryPolicy(plan.History); err != nil {
		return plan, err
	}
	if err := validateMetadataPolicy(plan.Metadata); err != nil {
		return plan, err
	}
	if err := validatePluginNodes(plan); err != nil {
		return plan, err
	}
	if err := validatePluginArtifacts(plan); err != nil {
		return plan, err
	}
	return plan, nil
}

func (p Plan) Digest() string {
	data, _ := json.Marshal(p)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type OriginalDecoder interface {
	SourceIndexDecoder
	RecordDecoder
	BusinessDecoder
}

// Preflight is evidence of source selection and conversion only. It must never
// be presented as evidence that the target has passed independent verification.
type Preflight struct {
	Status          string                 `json:"status"`
	CutoverReady    bool                   `json:"cutover_ready"`
	PlanDigest      string                 `json:"plan_digest"`
	SourceCommit    string                 `json:"source_commit"`
	Capture         SourceCapture          `json:"capture"`
	Catalog         SourceCatalog          `json:"catalog"`
	Selection       SourceSelection        `json:"selection"`
	Conversion      TargetRecordsReport    `json:"conversion"`
	PluginSettings  *PluginSettingsReport  `json:"plugin_settings,omitempty"`
	PluginArtifacts *PluginArtifactsReport `json:"plugin_artifacts,omitempty"`
}

// Prepare reads stopped original data and publishes a preflight only when all
// authority, source-format and supported business-conversion checks succeed.
func Prepare(ctx context.Context, plan Plan, w Workspace, source Source, decoder OriginalDecoder, progress func(uint64, string)) (result Preflight, err error) {
	if err := validatePluginArtifacts(plan); err != nil {
		return result, err
	}
	if err := validatePluginNodes(plan); err != nil {
		return result, err
	}
	if err := validateHistoryPolicy(plan.History); err != nil {
		return result, err
	}
	if err := validateMetadataPolicy(plan.Metadata); err != nil {
		return result, err
	}
	if err := validateMessagePolicy(plan.Messages); err != nil {
		return result, err
	}
	// Emit bounded stage boundaries so operators can attribute validation time
	// without message-level logs or treating a quiet process as a failed job.
	var stageName string
	var stageStart time.Time
	finishStage := func(stageErr error) {
		if progress == nil || stageName == "" {
			return
		}
		state := "completed"
		if stageErr != nil {
			state = "failed"
		}
		progress(0, fmt.Sprintf("%s %s after %s", stageName, state, time.Since(stageStart).Round(time.Millisecond)))
	}
	stage := func(name string) {
		finishStage(nil)
		stageName = name
		if progress != nil {
			stageStart = time.Now()
			progress(0, name+" started")
		}
	}
	defer func() { finishStage(err) }()
	result.PlanDigest = plan.Digest()
	result.SourceCommit = plan.SourceCommit
	stage("source capture")
	if result.Capture, err = CaptureSources(ctx, plan.Sources, source, w, progress); err != nil {
		return result, err
	}
	stage("plugin settings")
	if result.PluginSettings, err = PreparePluginSettings(ctx, plan, result.Capture, w, decoder); err != nil {
		return result, err
	}
	artifactSource, _ := source.(PluginArtifactSource)
	stage("plugin artifact capture")
	if err := CapturePluginArtifacts(ctx, plan, w, artifactSource); err != nil {
		return result, err
	}
	stage("plugin artifact validation")
	if result.PluginArtifacts, err = PreparePluginArtifacts(ctx, plan, result.Capture, w); err != nil {
		return result, err
	}
	stage("approved quarantine")
	w, err = prepareQuarantine(ctx, plan, result.Capture, w, decoder)
	if err != nil {
		return result, err
	}
	stage("empty-channel certification")
	decoder, err = certifyEmptyChannels(ctx, result.Capture, w, decoder, plan.Metadata)
	if err != nil {
		return result, err
	}
	stage("source identity catalog")
	if result.Catalog, err = BuildSourceCatalog(ctx, result.Capture, w, decoder); err != nil {
		return result, err
	}
	stage("quarantine catalog validation")
	if err = validateQuarantineCatalog(ctx, w); err != nil {
		return result, err
	}
	stage("source index validation")
	if err = validateSourceIndexes(ctx, result.Capture, plan.Sources, w, decoder, plan.Metadata, plan.Messages); err != nil {
		return result, err
	}
	stage("source authority and replica comparison")
	if result.Selection, err = selectSources(ctx, result.Capture, result.Catalog, w, decoder, plan.Exclusions, plan.Metadata, result.PluginArtifacts, plan.History, plan.Messages); err != nil {
		return result, err
	}
	stage("native record conversion")
	if result.Conversion, err = BuildTargetRecords(ctx, result.Selection, w, decoder); err != nil {
		return result, err
	}
	stage("prepare checkpoint")
	result.Status = "prepared"
	data, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	return result, w.Put(ctx, []transfer.SpoolRow{{Key: []byte("workflow/PREPARED"), Value: data}})
}
