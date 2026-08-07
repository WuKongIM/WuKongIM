// Package clouddeploy owns the procurement-independent offline deployment use case.
package clouddeploy

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"sort"
	"strings"
)

const (
	// IntentSchemaV1 is the fixed four-host native deployment intent.
	IntentSchemaV1 = "wukongim.cloud_deployment.bundle_intent/v1"
	// ManifestSchemaV1 is the content-addressed offline bundle contract.
	ManifestSchemaV1 = "wukongim.cloud_deployment.bundle/v1"
	intentName       = "deployment-intent.json"
	manifestName     = "bundle-manifest.json"
	maxBundleFiles   = 2048
	maxJSONBytes     = 1 << 20
)

var (
	// ErrInvalidBundle reports missing, unexpected, unsupported, or tampered content.
	ErrInvalidBundle = errors.New("internal/usecase/clouddeploy: invalid bundle")
)

// Directory is the narrow no-follow file boundary required by bundle policy.
// Infrastructure adapters must reject symlinks and non-regular entries.
type Directory interface {
	WriteFile(relative string, data []byte, mode uint32) error
	ReadFile(relative string, maxBytes int64) ([]byte, error)
	ReadPrefix(relative string, bytes int) ([]byte, error)
	Files(maxFiles int) ([]FileRecord, error)
}

// SecretFile declares one runtime-injected root-owned file that is never bundled.
type SecretFile struct {
	// Path is the exact absolute host path under /etc/wukongim/secrets.
	Path string `json:"path"`
	// Owner fixes the account that owns the runtime-injected file.
	Owner string `json:"owner"`
	// Mode is the required root-readable and non-world-readable permission mode.
	Mode uint32 `json:"mode"`
}

// Intent fixes all build-time deployment assumptions without Lease-specific state.
type Intent struct {
	// Schema selects the only accepted deployment intent.
	Schema string `json:"schema"`
	// SourceSHA identifies product binaries and compiled frontend assets.
	SourceSHA string `json:"source_sha"`
	// ControlSHA identifies the trusted workflow and deployment tooling revision.
	ControlSHA string `json:"control_sha"`
	// OperatingSystem, OperatingSystemVersion, and Architecture bind the image ABI.
	OperatingSystem        string `json:"operating_system"`
	OperatingSystemVersion string `json:"operating_system_version"`
	Architecture           string `json:"architecture"`
	// ServiceNodes and LoadNodes fix the four-host topology.
	ServiceNodes int `json:"service_nodes"`
	LoadNodes    int `json:"load_nodes"`
	// HashSlots, WorkloadSlotGroups, and Replicas fix cluster semantics.
	HashSlots          int `json:"hash_slots"`
	WorkloadSlotGroups int `json:"workload_slot_groups"`
	Replicas           int `json:"replicas"`
	// SecretFiles are runtime-only files created by the Deployment Action.
	SecretFiles []SecretFile `json:"secret_files"`
	// RequiredBaseTools must be proved from the selected Ubuntu image offline.
	RequiredBaseTools []string `json:"required_base_tools"`
	// OfflineBinaries are the complete executable payload carried by the bundle.
	OfflineBinaries []string `json:"offline_binaries"`
}

// FileRecord binds one clean path to its exact mode, size, and content digest.
type FileRecord struct {
	// Path is slash-separated and relative to the bundle root.
	Path string `json:"path"`
	// Mode is the exact Unix permission mode.
	Mode uint32 `json:"mode"`
	// Size is the exact file size in bytes.
	Size int64 `json:"size"`
	// SHA256 is the lowercase digest without a prefix.
	SHA256 string `json:"sha256"`
}

// Manifest binds the source, control plane, intent, and every bundle byte.
type Manifest struct {
	// Schema selects the only accepted offline bundle manifest.
	Schema string `json:"schema"`
	// SourceSHA and ControlSHA preserve the two independent revision identities.
	SourceSHA  string `json:"source_sha"`
	ControlSHA string `json:"control_sha"`
	// IntentSHA256 binds the canonical deployment-intent document.
	IntentSHA256 string `json:"intent_sha256"`
	// BundleDigest is the SHA-256 digest over the ordered file records.
	BundleDigest string `json:"bundle_digest"`
	// Files is the complete bundle inventory except this manifest.
	Files []FileRecord `json:"files"`
}

// DefaultIntent returns the closed Ubuntu 24.04 x86_64 four-host contract.
func DefaultIntent(sourceSHA, controlSHA string) Intent {
	return Intent{
		Schema: IntentSchemaV1, SourceSHA: sourceSHA, ControlSHA: controlSHA,
		OperatingSystem: "ubuntu", OperatingSystemVersion: "24.04", Architecture: "amd64",
		ServiceNodes: 3, LoadNodes: 1, HashSlots: 256, WorkloadSlotGroups: 12, Replicas: 3,
		SecretFiles: []SecretFile{
			{Path: "/etc/wukongim/secrets/node.env", Owner: "root", Mode: 0o600},
			{Path: "/etc/wukongim/secrets/load.env", Owner: "root", Mode: 0o600},
			{Path: "/etc/wukongim/secrets/analysis.env", Owner: "root", Mode: 0o600},
			{Path: "/etc/wukongim/secrets/analysis-cert.pem", Owner: "root", Mode: 0o600},
			{Path: "/etc/wukongim/secrets/analysis-key.pem", Owner: "root", Mode: 0o600},
		},
		RequiredBaseTools: []string{"awk", "bash", "blkid", "cat", "chmod", "chown", "curl", "date", "df", "dirname", "findmnt", "getconf", "grep", "head", "id", "install", "lsblk", "mkdir", "mkfs.ext4", "mount", "mv", "rm", "scp", "sed", "sha256sum", "sleep", "ssh", "stat", "sudo", "systemctl", "tail", "tar", "timedatectl", "timeout", "uname", "useradd"},
		OfflineBinaries:   []string{"caddy", "node_exporter", "prometheus", "wkanalysis", "wkbench", "wkcloudbundle", "wkcloudgate", "wkcloudhost", "wukongim"},
	}
}

// Seal writes canonical intent and native templates, validates the complete
// payload without starting services, then writes its content-addressed manifest.
func Seal(directory Directory, sourceSHA, controlSHA string) (Manifest, error) {
	intent := DefaultIntent(sourceSHA, controlSHA)
	if err := writeJSONFile(directory, intentName, intent, 0o644); err != nil {
		return Manifest{}, err
	}
	for filePath, content := range scaffoldFiles() {
		mode := uint32(0o644)
		if strings.HasPrefix(filePath, "scripts/") {
			mode = 0o755
		}
		if err := directory.WriteFile(filePath, []byte(content), mode); err != nil {
			return Manifest{}, err
		}
	}
	allFiles, err := directory.Files(maxBundleFiles)
	if err != nil {
		return Manifest{}, err
	}
	if _, hasManifest := findRecord(allFiles, manifestName); !hasManifest && len(allFiles) >= maxBundleFiles {
		return Manifest{}, fmt.Errorf("%w: manifest-inclusive file limit", ErrInvalidBundle)
	}
	if err := validateFiles(directory, intent, allFiles); err != nil {
		return Manifest{}, err
	}
	files := inventory(allFiles)
	intentRecord, ok := findRecord(files, intentName)
	if !ok {
		return Manifest{}, fmt.Errorf("%w: missing intent digest", ErrInvalidBundle)
	}
	manifest := Manifest{
		Schema: ManifestSchemaV1, SourceSHA: sourceSHA, ControlSHA: controlSHA,
		IntentSHA256: "sha256:" + intentRecord.SHA256, Files: files,
	}
	manifest.BundleDigest = digestRecords(files)
	if err := writeJSONFile(directory, manifestName, manifest, 0o644); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// Verify independently proves static semantics, the exact file inventory, and digest.
func Verify(directory Directory) (Manifest, error) {
	var manifest Manifest
	if err := readStrictJSON(directory, manifestName, &manifest); err != nil ||
		manifest.Schema != ManifestSchemaV1 || !validDigest(manifest.BundleDigest) || !validDigest(manifest.IntentSHA256) {
		return Manifest{}, fmt.Errorf("%w: malformed manifest", ErrInvalidBundle)
	}
	allFiles, err := directory.Files(maxBundleFiles)
	if err != nil {
		return Manifest{}, err
	}
	manifestRecord, ok := findRecord(allFiles, manifestName)
	if !ok || manifestRecord.Mode != 0o644 {
		return Manifest{}, fmt.Errorf("%w: manifest file", ErrInvalidBundle)
	}
	var intent Intent
	if err := readStrictJSON(directory, intentName, &intent); err != nil || !validIntent(intent) {
		return Manifest{}, fmt.Errorf("%w: deployment intent", ErrInvalidBundle)
	}
	if err := validateFiles(directory, intent, allFiles); err != nil {
		return Manifest{}, err
	}
	files := inventory(allFiles)
	intentRecord, ok := findRecord(files, intentName)
	if !ok || !slices.Equal(files, manifest.Files) || digestRecords(files) != manifest.BundleDigest ||
		"sha256:"+intentRecord.SHA256 != manifest.IntentSHA256 {
		return Manifest{}, fmt.Errorf("%w: content digest mismatch", ErrInvalidBundle)
	}
	if manifest.SourceSHA != intent.SourceSHA || manifest.ControlSHA != intent.ControlSHA {
		return Manifest{}, fmt.Errorf("%w: identity mismatch", ErrInvalidBundle)
	}
	return manifest, nil
}

// Validate performs no background work and proves the fixed native bundle contract.
func Validate(directory Directory) error {
	var intent Intent
	if err := readStrictJSON(directory, intentName, &intent); err != nil || !validIntent(intent) {
		return fmt.Errorf("%w: deployment intent", ErrInvalidBundle)
	}
	files, err := directory.Files(maxBundleFiles)
	if err != nil {
		return err
	}
	return validateFiles(directory, intent, files)
}

func validateFiles(directory Directory, intent Intent, files []FileRecord) error {
	byPath := make(map[string]FileRecord, len(files))
	for _, file := range files {
		if _, exists := byPath[file.Path]; exists {
			return fmt.Errorf("%w: duplicate file %s", ErrInvalidBundle, file.Path)
		}
		byPath[file.Path] = file
	}
	for _, filePath := range requiredFiles(intent) {
		record, ok := byPath[filePath]
		if !ok || record.Mode != expectedFileMode(filePath) {
			return fmt.Errorf("%w: required file %s", ErrInvalidBundle, filePath)
		}
	}
	for _, name := range intent.OfflineBinaries {
		filePath := "bin/" + name
		header, err := directory.ReadPrefix(filePath, 20)
		if err != nil || !linuxAMD64ELF(header) {
			return fmt.Errorf("%w: linux/amd64 binary %s", ErrInvalidBundle, name)
		}
	}
	for _, secret := range intent.SecretFiles {
		if secret.Owner != "root" || secret.Mode != 0o600 || !strings.HasPrefix(secret.Path, "/etc/wukongim/secrets/") || path.Clean(secret.Path) != secret.Path {
			return fmt.Errorf("%w: secret path", ErrInvalidBundle)
		}
		if _, exists := byPath[strings.TrimPrefix(secret.Path, "/")]; exists {
			return fmt.Errorf("%w: bundled secret material", ErrInvalidBundle)
		}
	}
	if err := rejectBundledSecretMaterial(files); err != nil {
		return err
	}
	workload, err := directory.ReadFile("config/chat-lifecycle.yaml", maxJSONBytes)
	if err != nil || !validFormalWorkload(string(workload)) {
		return fmt.Errorf("%w: formal chat-lifecycle config", ErrInvalidBundle)
	}
	return rejectContainerDependency(directory, intent, files)
}

func validFormalWorkload(content string) bool {
	required := []string{
		"profile: formal", "workers: 3", "logical_slot_groups: 12", "hash_slots: 256",
		"slot_replicas: 3", "channel_replicas: 3", "version: 0",
		"minimum_data_filesystem_bytes: 500000000000",
	}
	for _, fragment := range required {
		if !strings.Contains(content, fragment) {
			return false
		}
	}
	return !strings.Contains(content, "minimum_data_filesystem_bytes: 1000000000000")
}

func validIntent(intent Intent) bool {
	want := DefaultIntent(intent.SourceSHA, intent.ControlSHA)
	return intent.Schema == IntentSchemaV1 && validSHA(intent.SourceSHA) && validSHA(intent.ControlSHA) &&
		intent.OperatingSystem == want.OperatingSystem && intent.OperatingSystemVersion == want.OperatingSystemVersion &&
		intent.Architecture == want.Architecture && intent.ServiceNodes == want.ServiceNodes && intent.LoadNodes == want.LoadNodes &&
		intent.HashSlots == want.HashSlots && intent.WorkloadSlotGroups == want.WorkloadSlotGroups && intent.Replicas == want.Replicas &&
		slices.Equal(intent.SecretFiles, want.SecretFiles) && slices.Equal(intent.RequiredBaseTools, want.RequiredBaseTools) &&
		slices.Equal(intent.OfflineBinaries, want.OfflineBinaries)
}

func requiredFiles(intent Intent) []string {
	paths := []string{intentName, "assets/demo/index.html", "assets/manager/index.html",
		"config/Caddyfile.tmpl", "config/chat-lifecycle.yaml", "config/prometheus.yml.tmpl", "config/wukongim.toml.tmpl",
		"scripts/collect-evidence.sh", "scripts/collect-process-metrics.sh", "scripts/verify-base-tools.sh", "scripts/wait-coordinator-dependencies.sh",
		"systemd/caddy.service", "systemd/node-exporter.service", "systemd/prometheus.service",
		"systemd/wkanalysis.service", "systemd/wkbench-coordinator.service", "systemd/wkbench-worker@.service",
		"systemd/wkbench-host-metrics.service",
		"systemd/wukongim-process-metrics.service", "systemd/wukongim-evidence.service",
		"systemd/wukongim-evidence.timer", "systemd/wukongim.service"}
	for _, name := range intent.OfflineBinaries {
		paths = append(paths, "bin/"+name)
	}
	return paths
}

func expectedFileMode(filePath string) uint32 {
	if strings.HasPrefix(filePath, "bin/") || strings.HasPrefix(filePath, "scripts/") {
		return 0o755
	}
	return 0o644
}

func rejectContainerDependency(directory Directory, intent Intent, files []FileRecord) error {
	allowedBinaries := make(map[string]struct{}, len(intent.OfflineBinaries))
	for _, name := range intent.OfflineBinaries {
		allowedBinaries["bin/"+name] = struct{}{}
	}
	for _, file := range files {
		lowerPath := strings.ToLower(file.Path)
		base := path.Base(lowerPath)
		first, _, _ := strings.Cut(lowerPath, "/")
		if first != "assets" && first != "bin" && first != "config" && first != "scripts" && first != "systemd" &&
			lowerPath != intentName && lowerPath != manifestName {
			return fmt.Errorf("%w: unexpected bundle path %s", ErrInvalidBundle, file.Path)
		}
		if first == "assets" && !strings.HasPrefix(lowerPath, "assets/demo/") && !strings.HasPrefix(lowerPath, "assets/manager/") {
			return fmt.Errorf("%w: unexpected asset path %s", ErrInvalidBundle, file.Path)
		}
		if strings.Contains(lowerPath, "docker") || strings.Contains(lowerPath, "containerd") || strings.Contains(lowerPath, "podman") ||
			(strings.Contains(base, "compose") && (strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml"))) {
			return fmt.Errorf("%w: container artifact %s", ErrInvalidBundle, file.Path)
		}
		if strings.HasPrefix(lowerPath, "bin/") {
			if _, ok := allowedBinaries[lowerPath]; !ok {
				return fmt.Errorf("%w: unexpected executable %s", ErrInvalidBundle, file.Path)
			}
		}
		if !strings.HasPrefix(lowerPath, "config/") && !strings.HasPrefix(lowerPath, "scripts/") && !strings.HasPrefix(lowerPath, "systemd/") {
			continue
		}
		data, err := directory.ReadFile(file.Path, maxJSONBytes)
		if err != nil {
			return fmt.Errorf("%w: inspect runtime file %s", ErrInvalidBundle, file.Path)
		}
		lower := strings.ToLower(string(data))
		for _, forbidden := range []string{"docker", "containerd", "podman"} {
			if strings.Contains(lower, forbidden) {
				return fmt.Errorf("%w: container dependency in %s", ErrInvalidBundle, file.Path)
			}
		}
	}
	return nil
}

func rejectBundledSecretMaterial(files []FileRecord) error {
	for _, file := range files {
		lower := strings.ToLower(file.Path)
		base := path.Base(lower)
		if strings.Contains("/"+lower+"/", "/secrets/") || base == "authorized_keys" ||
			strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") ||
			strings.HasSuffix(base, ".p12") || strings.HasSuffix(base, ".env") {
			return fmt.Errorf("%w: bundled secret-shaped path %s", ErrInvalidBundle, file.Path)
		}
	}
	return nil
}

func linuxAMD64ELF(header []byte) bool {
	return len(header) >= 20 && string(header[:4]) == "\x7fELF" && header[4] == 2 && header[5] == 1 &&
		(header[7] == 0 || header[7] == 3) &&
		(binary.LittleEndian.Uint16(header[16:18]) == 2 || binary.LittleEndian.Uint16(header[16:18]) == 3) &&
		binary.LittleEndian.Uint16(header[18:20]) == 62
}

func inventory(all []FileRecord) []FileRecord {
	records := make([]FileRecord, 0, len(all))
	for _, record := range all {
		if record.Path != manifestName {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Path < records[j].Path })
	return records
}

func findRecord(records []FileRecord, filePath string) (FileRecord, bool) {
	for _, record := range records {
		if record.Path == filePath {
			return record, true
		}
	}
	return FileRecord{}, false
}

func digestRecords(records []FileRecord) string {
	digest := sha256.New()
	for _, record := range records {
		fmt.Fprintf(digest, "%s\x00%d\x00%d\x00%s\n", record.Path, record.Mode, record.Size, record.SHA256)
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil))
}

func writeJSONFile(directory Directory, relative string, value any, mode uint32) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return directory.WriteFile(relative, append(data, '\n'), mode)
}

func readStrictJSON(directory Directory, relative string, value any) error {
	data, err := directory.ReadFile(relative, maxJSONBytes)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrInvalidBundle
	}
	return nil
}

func validSHA(value string) bool {
	if len(value) != 40 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validDigest(value string) bool {
	encoded := strings.TrimPrefix(value, "sha256:")
	if encoded == value || len(encoded) != 64 {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil
}
