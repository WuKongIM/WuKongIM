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
	"reflect"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

const pluginArtifactChunkBytes = 1 << 20

// PluginArtifactSpec identifies one immutable executable from a stopped source.
// Bytes and SHA256 are mandatory; paths are read only during source capture.
type PluginArtifactSpec struct {
	SourceNode uint64 `json:"source_node"`
	PluginNo   string `json:"plugin_no"`
	Path       string `json:"path"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
	// Profile explicitly selects one code-defined, exact-program mapping.
	// Empty preserves the bytes but cannot authorize target execution.
	Profile string `json:"profile,omitempty"`
}

// PluginArtifactSource opens regular original executable files without launching
// them. The adapter returns their original permission bits for source provenance.
type PluginArtifactSource interface {
	OpenPluginArtifact(context.Context, PluginArtifactSpec) (io.ReadCloser, uint32, error)
}

type CapturedPluginArtifact struct {
	Spec   PluginArtifactSpec `json:"source"`
	Mode   uint32             `json:"original_mode"`
	Chunks uint64             `json:"chunks"`
}

// PluginArtifactsReport binds executable bytes only, never business compatibility.
// The complete plan binds source-to-target assignments and native filenames.
type PluginArtifactsReport struct {
	PlanDigest    string                       `json:"plan_digest"`
	Files         []CapturedPluginArtifact     `json:"files"`
	Targets       []PluginArtifactAssignment   `json:"targets"`
	Compatibility *PluginCompatibilityEvidence `json:"compatibility,omitempty"`
	Digest        string                       `json:"digest"`
}

type PluginArtifactAssignment struct {
	TargetNode uint64 `json:"target_node"`
	SourceNode uint64 `json:"source_node"`
	PluginNo   string `json:"plugin_no"`
}

func validatePluginArtifacts(p Plan) error {
	if len(p.PluginArtifacts) == 0 {
		return nil
	}
	if len(p.PluginArtifacts) > 1024 || len(p.PluginNodes) == 0 {
		return errors.New("plugin artifacts require node assignments and at most 1024 source files")
	}
	sources, seen := map[uint64]bool{}, map[string]bool{}
	for _, n := range p.Sources {
		sources[n.NodeID] = true
	}
	for _, f := range p.PluginArtifacts {
		key := pluginArtifactPrefix(f)
		if !sources[f.SourceNode] || !validPluginArtifactSpec(f) || seen[key] {
			return errors.New("invalid or duplicate plugin artifact identity, size or SHA256")
		}
		seen[key] = true
	}
	return nil
}

func validPluginArtifactSpec(f PluginArtifactSpec) bool {
	hash, err := hex.DecodeString(f.SHA256)
	return f.SourceNode != 0 && mappedPluginNo.MatchString(f.PluginNo) && f.PluginNo != "." && f.PluginNo != ".." && len(f.PluginNo) <= 200 && filepath.IsAbs(f.Path) && f.Bytes > 0 && f.Bytes <= 512<<20 && err == nil && len(hash) == 32 && hex.EncodeToString(hash) == f.SHA256 && (f.Profile == "" || knownPluginProfile(f.Profile))
}

func pluginArtifactPrefix(s PluginArtifactSpec) string {
	return fmt.Sprintf("plugin-artifacts/v1/%020d/%x/", s.SourceNode, []byte(s.PluginNo))
}

// CapturePluginArtifacts streams exact source bytes into immutable bounded rows.
// A mismatched, interrupted or changed source cannot publish a file descriptor.
func CapturePluginArtifacts(ctx context.Context, p Plan, w Workspace, source PluginArtifactSource) error {
	if err := validatePluginArtifacts(p); err != nil {
		return err
	}
	if len(p.PluginArtifacts) == 0 {
		return nil
	}
	if ctx == nil || w == nil || source == nil {
		return errors.New("plugin artifacts require a stopped-file reader")
	}
	for _, spec := range p.PluginArtifacts {
		if err := capturePluginArtifact(ctx, spec, w, source); err != nil {
			return fmt.Errorf("source node %d plugin artifact capture: %w", spec.SourceNode, err)
		}
	}
	return nil
}

func capturePluginArtifact(ctx context.Context, spec PluginArtifactSpec, w Workspace, source PluginArtifactSource) (err error) {
	f, mode, err := source.OpenPluginArtifact(ctx, spec)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	if mode & ^uint32(0777) != 0 || mode&0111 == 0 {
		return errors.New("source plugin is not an ordinary executable")
	}
	h := sha256.New()
	buffer := make([]byte, pluginArtifactChunkBytes)
	var count uint64
	var size int64
	for size < spec.Bytes {
		if err := ctx.Err(); err != nil {
			return err
		}
		want := min(int64(len(buffer)), spec.Bytes-size)
		n, err := io.ReadFull(f, buffer[:want])
		if err != nil {
			return errors.New("source plugin is shorter than its planned size")
		}
		h.Write(buffer[:n])
		key := fmt.Sprintf("%schunks/%08d", pluginArtifactPrefix(spec), count)
		if err := w.Put(ctx, []transfer.SpoolRow{{Key: []byte(key), Value: buffer[:n]}}); err != nil {
			return err
		}
		size += int64(n)
		count++
	}
	if n, err := io.ReadFull(f, buffer[:1]); n != 0 || err != io.EOF {
		return errors.New("source plugin exceeds its planned size")
	}
	if hex.EncodeToString(h.Sum(nil)) != spec.SHA256 {
		return errors.New("source plugin SHA256 differs from plan")
	}
	data, err := json.Marshal(CapturedPluginArtifact{Spec: spec, Mode: mode, Chunks: count})
	if err != nil {
		return err
	}
	return w.Put(ctx, []transfer.SpoolRow{{Key: []byte(pluginArtifactPrefix(spec) + "descriptor"), Value: data}})
}

// WalkPluginArtifact rechecks every archived chunk against the original plan.
// A consumer may stage bytes while walking, but must not publish before success.
func WalkPluginArtifact(ctx context.Context, w Workspace, spec PluginArtifactSpec, visit func([]byte) error) (out CapturedPluginArtifact, err error) {
	if ctx == nil || w == nil || !validPluginArtifactSpec(spec) {
		return out, errors.New("invalid captured plugin specification")
	}
	data, found, err := w.Get(ctx, []byte(pluginArtifactPrefix(spec)+"descriptor"))
	if err != nil {
		return out, err
	}
	if !found {
		return out, errors.New("captured plugin descriptor missing")
	}
	if err := decodeArchiveJSON(data, &out); err != nil {
		return out, err
	}
	if out.Spec != spec || out.Mode & ^uint32(0777) != 0 || out.Mode&0111 == 0 || out.Chunks != uint64((spec.Bytes+pluginArtifactChunkBytes-1)/pluginArtifactChunkBytes) {
		return out, errors.New("captured plugin descriptor differs from plan")
	}
	h := sha256.New()
	var size int64
	var count uint64
	err = w.Walk(ctx, []byte(pluginArtifactPrefix(spec)+"chunks/"), func(row transfer.SpoolRow) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		want := min(int64(pluginArtifactChunkBytes), spec.Bytes-size)
		if count >= out.Chunks || string(row.Key) != fmt.Sprintf("%schunks/%08d", pluginArtifactPrefix(spec), count) || int64(len(row.Value)) != want {
			return errors.New("captured plugin chunk is missing, extra or out of order")
		}
		h.Write(row.Value)
		size += int64(len(row.Value))
		count++
		if visit != nil {
			return visit(row.Value)
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	if size != spec.Bytes || count != out.Chunks || hex.EncodeToString(h.Sum(nil)) != spec.SHA256 {
		return out, errors.New("captured plugin size or SHA256 differs from plan")
	}
	return out, nil
}

// PreparePluginArtifacts reconstructs the report from captured original bytes,
// rejecting undeclared files and requiring each source registration to exist.
func PreparePluginArtifacts(ctx context.Context, p Plan, capture SourceCapture, w Workspace) (*PluginArtifactsReport, error) {
	if err := validatePluginArtifacts(p); err != nil {
		return nil, err
	}
	if len(p.PluginArtifacts) == 0 {
		err := w.Walk(ctx, []byte("plugin-artifacts/"), func(transfer.SpoolRow) error { return errors.New("undeclared plugin artifact rows") })
		return nil, err
	}
	report := &PluginArtifactsReport{PlanDigest: p.Digest()}
	specs := append([]PluginArtifactSpec(nil), p.PluginArtifacts...)
	sort.Slice(specs, func(i, j int) bool { return pluginArtifactPrefix(specs[i]) < pluginArtifactPrefix(specs[j]) })
	var expectedRows uint64
	for _, spec := range specs {
		key := "plugin-settings-original/v2/" + capture.Digest + "/" + p.Digest() + fmt.Sprintf("/%020d/%x", spec.SourceNode, []byte(spec.PluginNo))
		_, found, err := w.Get(ctx, []byte(key))
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, errors.New("plugin artifact has no original source registration")
		}
		f, err := WalkPluginArtifact(ctx, w, spec, nil)
		if err != nil {
			return nil, err
		}
		report.Files = append(report.Files, f)
		expectedRows += f.Chunks + 1
	}
	var rows uint64
	if err := w.Walk(ctx, []byte("plugin-artifacts/"), func(transfer.SpoolRow) error {
		rows++
		if rows > expectedRows {
			return errors.New("unexpected captured plugin artifact rows")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if rows != expectedRows {
		return nil, errors.New("captured plugin artifact row count mismatch")
	}
	var err error
	report.Compatibility, err = certifyPluginProfile(ctx, p, capture, w)
	if err != nil {
		return nil, err
	}
	for _, node := range p.PluginNodes {
		for _, spec := range specs {
			if spec.SourceNode == node.SourceNode {
				if len(report.Targets) >= 65536 {
					return nil, errors.New("plugin executable assignments exceed 65536-file bound")
				}
				report.Targets = append(report.Targets, PluginArtifactAssignment{TargetNode: node.TargetNode, SourceNode: spec.SourceNode, PluginNo: spec.PluginNo})
			}
		}
	}
	sort.Slice(report.Targets, func(i, j int) bool {
		a, b := report.Targets[i], report.Targets[j]
		if a.TargetNode != b.TargetNode {
			return a.TargetNode < b.TargetNode
		}
		return a.PluginNo < b.PluginNo
	})
	data, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	report.Digest = diagnosticSHA(data)
	return report, nil
}

// ValidatePluginArtifactsReport rejects changed descriptors, bytes or assignments
// before an importer admits any output directory. Verification also rederives
// assignments from the original plan instead of trusting this derived report.
func ValidatePluginArtifactsReport(ctx context.Context, w Workspace, report *PluginArtifactsReport) error {
	if report == nil {
		return nil
	}
	if report.Compatibility == nil || !knownPluginProfile(report.Compatibility.Profile) || len(report.Compatibility.SourceRows) != len(report.Files) {
		return errors.New("plugin executables require a verified business compatibility profile")
	}
	copy := *report
	copy.Digest = ""
	data, err := json.Marshal(copy)
	if err != nil {
		return err
	}
	if report.PlanDigest == "" || len(report.Files) == 0 || len(report.Files) > 1024 || len(report.Targets) > 65536 || diagnosticSHA(data) != report.Digest {
		return errors.New("plugin artifact report digest mismatch")
	}
	seen := map[string]bool{}
	for _, file := range report.Files {
		if file.Spec.Profile != report.Compatibility.Profile || !matchesPluginProfile(file.Spec) {
			return errors.New("plugin executable differs from its verified profile")
		}
		key := pluginArtifactPrefix(file.Spec)
		if seen[key] {
			return errors.New("duplicate captured plugin descriptor")
		}
		seen[key] = true
		got, err := WalkPluginArtifact(ctx, w, file.Spec, nil)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, file) {
			return errors.New("plugin report differs from captured descriptor")
		}
	}
	assignments := map[string]bool{}
	for _, a := range report.Targets {
		key := fmt.Sprintf("%020d/%s", a.TargetNode, a.PluginNo)
		if a.TargetNode == 0 || assignments[key] || !seen[pluginArtifactPrefix(PluginArtifactSpec{SourceNode: a.SourceNode, PluginNo: a.PluginNo})] {
			return errors.New("invalid plugin artifact target assignment")
		}
		assignments[key] = true
	}
	return nil
}
