// Package deploy owns the immutable cloud deployment bundle and bootstrap gate.
package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// ManifestSchemaV1 is the only accepted bundle manifest schema.
	ManifestSchemaV1 = "wukongim/cloud-deployment-bundle/v1"
	manifestName     = "manifest.json"
	maxBundleFiles   = 128
)

var (
	// ErrInvalidBundle reports malformed, incomplete, or tampered bundle content.
	ErrInvalidBundle = errors.New("internal/infra/cloudsim/deploy: invalid bundle")
)

// BundleSpec is the non-secret, run-specific deployment input.
type BundleSpec struct {
	// RunID is the exact Simulation Run identity.
	RunID string `json:"run_id"`
	// SourceSHA is the trusted main commit used to build static binaries.
	SourceSHA string `json:"source_sha"`
	// ScenarioPath is one repository-owned wkbench/v1 YAML profile.
	ScenarioPath string `json:"scenario_path"`
	// ScenarioDigest is the canonical effective scenario digest.
	ScenarioDigest string `json:"scenario_digest"`
	// Duration is the selected allowlisted active workload duration.
	Duration time.Duration `json:"duration"`
	// PrivateIPv4 contains fixed unique addresses for node-1, node-2, node-3, and sim.
	PrivateIPv4 map[string]string `json:"private_ipv4"`
	// SimulatorSourceIPv4 contains the simulator's provider-assigned explicit TCP source pool.
	SimulatorSourceIPv4 []string `json:"simulator_source_ipv4"`
	// PublicViewEnabled includes the simulator-only public Cloud View binary,
	// configuration, and service unit when true.
	PublicViewEnabled bool `json:"public_view_enabled"`
}

// FileRecord binds one relative path, mode, size, and SHA-256 digest.
type FileRecord struct {
	// Path is the clean bundle-relative file path.
	Path string `json:"path"`
	// Mode is the exact deployed permission mode.
	Mode uint32 `json:"mode"`
	// Size is the exact file size in bytes.
	Size int64 `json:"size"`
	// SHA256 is the lowercase content digest.
	SHA256 string `json:"sha256"`
}

// Manifest binds every byte deployed to all four hosts.
type Manifest struct {
	// Schema is the exact versioned bundle manifest contract.
	Schema string `json:"schema"`
	// RunID is the exact Simulation Run identity.
	RunID string `json:"run_id"`
	// SourceSHA is the immutable commit used to build the binaries.
	SourceSHA string `json:"source_sha"`
	// ScenarioDigest is the canonical effective workload identity.
	ScenarioDigest string `json:"scenario_digest"`
	// BundleDigest is the digest over the ordered File records.
	BundleDigest string `json:"bundle_digest"`
	// Files contains every deployed file except the manifest itself.
	Files []FileRecord `json:"files"`
}

// Render writes deterministic role configs, Prometheus config, and systemd units.
// Static binaries must already exist under root/bin and are never compiled on a host.
func Render(root string, spec BundleSpec) error {
	if err := validateSpec(spec); err != nil {
		return err
	}
	runtimeProfile, err := nodeRuntimeProfileForScenario(spec.ScenarioPath)
	if err != nil {
		return err
	}
	requiredBinaries := []string{"wukongim", "wkbench", "wkanalysis", "prometheus", "node_exporter"}
	if spec.PublicViewEnabled {
		requiredBinaries = append(requiredBinaries, "wkcloudview")
	}
	for _, name := range requiredBinaries {
		info, err := os.Stat(filepath.Join(root, "bin", name))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("%w: missing static binary %s", ErrInvalidBundle, name)
		}
	}
	for _, dir := range []string{"config", "scripts", "systemd"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			return err
		}
	}
	for index, role := range []string{"node-1", "node-2", "node-3"} {
		content := nodeConfig(index+1, spec.PrivateIPv4, runtimeProfile)
		if err := writeBundleFile(root, filepath.Join("config", role+".toml"), []byte(content), 0o640); err != nil {
			return err
		}
	}
	if err := renderScenario(root, spec); err != nil {
		return err
	}
	files := map[string]string{
		"config/prometheus.yml": prometheusConfig(spec.PrivateIPv4),
		"config/target.yaml":    targetConfig(spec.PrivateIPv4),
		"config/workers.yaml":   workerConfig(spec.SimulatorSourceIPv4),
	}
	if spec.PublicViewEnabled {
		files["config/cloud-view.json"] = cloudViewConfig(spec.RunID, spec.PrivateIPv4)
	}
	for name, content := range files {
		if err := writeBundleFile(root, name, []byte(content), 0o640); err != nil {
			return err
		}
	}
	if err := writeBundleFile(root, filepath.Join("scripts", "wukongim-cgroup-metrics.sh"), []byte(cgroupMetricsCollector()), 0o755); err != nil {
		return err
	}
	for name, content := range systemdUnits(spec.PublicViewEnabled, spec.Duration) {
		if err := writeBundleFile(root, filepath.Join("systemd", name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// nodeRuntimeProfileForScenario returns only reviewed per-scale node overrides
// declared by the effective scenario rather than inferred from a filename.
func nodeRuntimeProfileForScenario(path string) (nodeRuntimeProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nodeRuntimeProfile{}, err
	}
	var document struct {
		Objectives struct {
			Scale string `yaml:"scale"`
		} `yaml:"objectives"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nodeRuntimeProfile{}, fmt.Errorf("%w: scenario YAML: %v", ErrInvalidBundle, err)
	}
	switch strings.ToLower(strings.TrimSpace(document.Objectives.Scale)) {
	case "small":
		return nodeRuntimeProfile{authorityCacheMaxRows: cloudSmallAuthorityCacheMaxRows}, nil
	case "medium":
		return nodeRuntimeProfile{
			authorityCacheMaxRows:      cloudMediumAuthorityCacheMaxRows,
			recipientWorkerConcurrency: cloudMediumRecipientWorkerConcurrency,
		}, nil
	case "large":
		return nodeRuntimeProfile{authorityCacheMaxRows: cloudLargeAuthorityCacheMaxRows}, nil
	default:
		return nodeRuntimeProfile{}, fmt.Errorf("%w: scenario objectives.scale must be small, medium, or large", ErrInvalidBundle)
	}
}

// Seal inventories all bundle files and writes a self-contained manifest.
func Seal(root string, identity BundleSpec) (Manifest, error) {
	files, err := inventoryFiles(root)
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{
		Schema: ManifestSchemaV1, RunID: identity.RunID, SourceSHA: identity.SourceSHA,
		ScenarioDigest: identity.ScenarioDigest, Files: files,
	}
	manifest.BundleDigest = digestRecords(files)
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	data = append(data, '\n')
	if err := writeBundleFile(root, manifestName, data, 0o644); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// Verify proves the manifest identity, exact file set, modes, sizes, and digests.
func Verify(root string) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(root, manifestName))
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: read manifest: %v", ErrInvalidBundle, err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil || manifest.Schema != ManifestSchemaV1 || manifest.BundleDigest == "" {
		return Manifest{}, fmt.Errorf("%w: malformed manifest", ErrInvalidBundle)
	}
	files, err := inventoryFiles(root)
	if err != nil {
		return Manifest{}, err
	}
	if !recordsEqual(files, manifest.Files) || digestRecords(files) != manifest.BundleDigest {
		return Manifest{}, fmt.Errorf("%w: content digest mismatch", ErrInvalidBundle)
	}
	return manifest, nil
}

func validateSpec(spec BundleSpec) error {
	if strings.TrimSpace(spec.RunID) == "" || len(spec.SourceSHA) != 40 || !strings.HasPrefix(spec.ScenarioDigest, "sha256:") ||
		(spec.Duration != 30*time.Minute && spec.Duration != 2*time.Hour && spec.Duration != 24*time.Hour && spec.Duration != 48*time.Hour && spec.Duration != 168*time.Hour) {
		return ErrInvalidBundle
	}
	seen := make(map[string]struct{}, 4)
	for _, role := range []string{"node-1", "node-2", "node-3", "sim"} {
		address := strings.TrimSpace(spec.PrivateIPv4[role])
		if address == "" {
			return ErrInvalidBundle
		}
		if _, duplicate := seen[address]; duplicate {
			return ErrInvalidBundle
		}
		seen[address] = struct{}{}
	}
	if len(spec.SimulatorSourceIPv4) == 0 || spec.SimulatorSourceIPv4[0] != spec.PrivateIPv4["sim"] {
		return ErrInvalidBundle
	}
	for _, address := range spec.SimulatorSourceIPv4[1:] {
		if strings.TrimSpace(address) == "" {
			return ErrInvalidBundle
		}
		if _, duplicate := seen[address]; duplicate {
			return ErrInvalidBundle
		}
		seen[address] = struct{}{}
	}
	return nil
}

func inventoryFiles(root string) ([]FileRecord, error) {
	records := make([]FileRecord, 0, 32)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == "." || relative == manifestName {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlink %s", ErrInvalidBundle, relative)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || len(records) >= maxBundleFiles {
			return ErrInvalidBundle
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		digest := sha256.New()
		_, copyErr := io.Copy(digest, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
		records = append(records, FileRecord{Path: filepath.ToSlash(relative), Mode: uint32(info.Mode().Perm()), Size: info.Size(), SHA256: hex.EncodeToString(digest.Sum(nil))})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Path < records[j].Path })
	return records, nil
}

func digestRecords(records []FileRecord) string {
	digest := sha256.New()
	for _, record := range records {
		fmt.Fprintf(digest, "%s\x00%d\x00%d\x00%s\n", record.Path, record.Mode, record.Size, record.SHA256)
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil))
}

func recordsEqual(left, right []FileRecord) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func renderScenario(root string, spec BundleSpec) error {
	data, err := os.ReadFile(spec.ScenarioPath)
	if err != nil {
		return err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("%w: scenario YAML: %v", ErrInvalidBundle, err)
	}
	if !setYAMLScalar(&document, []string{"run", "duration"}, spec.Duration.String()) {
		return fmt.Errorf("%w: scenario lacks run.duration", ErrInvalidBundle)
	}
	if !setYAMLScalar(&document, []string{"run", "id"}, spec.RunID) {
		return fmt.Errorf("%w: scenario lacks run.id", ErrInvalidBundle)
	}
	if !setYAMLScalar(&document, []string{"run", "report_dir"}, "/var/lib/wukongim-cloud/reports/"+spec.RunID) {
		return fmt.Errorf("%w: scenario lacks run.report_dir", ErrInvalidBundle)
	}
	output, err := yaml.Marshal(&document)
	if err != nil {
		return err
	}
	return writeBundleFile(root, "config/scenario.yaml", output, 0o640)
}

func setYAMLScalar(node *yaml.Node, path []string, value string) bool {
	if node == nil || len(path) == 0 {
		return false
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		return setYAMLScalar(node.Content[0], path, value)
	}
	if node.Kind != yaml.MappingNode {
		return false
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value != path[0] {
			continue
		}
		if len(path) == 1 {
			node.Content[index+1].Kind = yaml.ScalarNode
			node.Content[index+1].Tag = "!!str"
			node.Content[index+1].Value = value
			return true
		}
		return setYAMLScalar(node.Content[index+1], path[1:], value)
	}
	return false
}

func writeBundleFile(root, relative string, data []byte, mode fs.FileMode) error {
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}
