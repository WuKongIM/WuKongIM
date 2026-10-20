package conversation_qps

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	productconfig "github.com/WuKongIM/WuKongIM/internal/config"
	"github.com/pelletier/go-toml/v2"
)

const fixedHostPreparationAge = 180 * time.Second

// validateFixedHostAffinity checks the kernel CPU-list syntax and cardinality;
// GOMAXPROCS alone does not prove the diagnostic has four assigned CPUs.
func validateFixedHostAffinity(affinity string) error {
	seen := map[uint64]bool{}
	for _, part := range strings.Split(affinity, ",") {
		first, last, isRange := strings.Cut(part, "-")
		start, err := strconv.ParseUint(first, 10, 32)
		if err != nil {
			return fmt.Errorf("invalid process CPU affinity")
		}
		end := start
		if isRange {
			end, err = strconv.ParseUint(last, 10, 32)
		}
		if err != nil || end < start || end-start > 3 {
			return fmt.Errorf("process CPU affinity is not exactly four CPUs")
		}
		for cpu := start; cpu <= end; cpu++ {
			if seen[cpu] {
				return fmt.Errorf("duplicate process CPU affinity")
			}
			seen[cpu] = true
		}
	}
	if len(seen) != 4 {
		return fmt.Errorf("process CPU affinity is not exactly four CPUs")
	}
	return nil
}

// fixedHostPreparationWait refuses late preparation rather than silently moving
// the common process-age target. Its inputs retain Go's monotonic clock.
func fixedHostPreparationWait(start, prepared time.Time) (time.Duration, error) {
	wait := start.Add(fixedHostPreparationAge).Sub(prepared)
	if wait < 0 {
		return 0, fmt.Errorf("fixed-host preparation exceeded 180-second process-age target by %s", -wait)
	}
	return wait, nil
}

// parseFixedHostProcess retains the kernel process start tick and observed CPU
// affinity; comm is parenthesized and may itself contain spaces or parentheses.
func parseFixedHostProcess(stat, status string) (uint64, string, error) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, "", fmt.Errorf("proc stat comm delimiter missing")
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) <= 19 {
		return 0, "", fmt.Errorf("proc stat starttime missing")
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || ticks == 0 {
		return 0, "", fmt.Errorf("proc stat starttime invalid")
	}
	for _, line := range strings.Split(status, "\n") {
		if value, ok := strings.CutPrefix(line, "Cpus_allowed_list:"); ok && strings.TrimSpace(value) != "" {
			return ticks, strings.TrimSpace(value), nil
		}
	}
	return 0, "", fmt.Errorf("proc status affinity missing")
}

// fixedHostStartEstimate maps kernel boot-relative process age onto a monotonic
// read bracket. The error bound includes uptime's 10ms display precision, one
// process-clock tick and half the userspace read interval.
func fixedHostStartEstimate(before, after time.Time, uptime string, tickHz, startTicks uint64) (time.Time, time.Duration, error) {
	fields := strings.Fields(uptime)
	if len(fields) == 0 || tickHz == 0 || startTicks == 0 || after.Before(before) {
		return time.Time{}, 0, fmt.Errorf("invalid process-age clock inputs")
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	age := seconds - float64(startTicks)/float64(tickHz)
	if err != nil || math.IsNaN(age) || math.IsInf(age, 0) || age < 0 || age > float64(math.MaxInt64)/float64(time.Second) {
		return time.Time{}, 0, fmt.Errorf("invalid process age from uptime/starttime")
	}
	halfRead := after.Sub(before) / 2
	uncertainty := halfRead + 10*time.Millisecond + time.Duration(float64(time.Second)/float64(tickHz))
	return before.Add(halfRead).Add(-time.Duration(age * float64(time.Second))), uncertainty, nil
}

type fixedHostCgroupLocation struct {
	Membership string `json:"membership"`
	MountRoot  string `json:"mount_root"`
	MountPoint string `json:"mount_point"`
	Directory  string `json:"directory"`
}

// resolveFixedHostCgroup uses the process's own membership and mount namespace,
// rather than assuming /sys/fs/cgroup is its resource-control directory.
func resolveFixedHostCgroup(cgroup, mountinfo string) (fixedHostCgroupLocation, error) {
	var membership string
	for _, line := range strings.Split(cgroup, "\n") {
		if value, ok := strings.CutPrefix(line, "0::"); ok {
			membership = value
		}
	}
	validPath := func(path string) bool {
		return filepath.IsAbs(path) && filepath.Clean(path) == path
	}
	if !validPath(membership) {
		return fixedHostCgroupLocation{}, fmt.Errorf("valid unified cgroup membership missing")
	}
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	var chosen fixedHostCgroupLocation
	for _, line := range strings.Split(mountinfo, "\n") {
		left, right, ok := strings.Cut(line, " - ")
		before, after := strings.Fields(left), strings.Fields(right)
		if !ok || len(before) < 5 || len(after) < 1 || after[0] != "cgroup2" {
			continue
		}
		root, mount := unescape.Replace(before[3]), unescape.Replace(before[4])
		if !validPath(root) || !validPath(mount) {
			continue
		}
		if membership != root && !strings.HasPrefix(membership, strings.TrimSuffix(root, "/")+"/") {
			continue
		}
		if chosen.MountRoot == "" || len(root) > len(chosen.MountRoot) {
			relative := strings.TrimPrefix(strings.TrimPrefix(membership, root), "/")
			chosen = fixedHostCgroupLocation{Membership: membership, MountRoot: root, MountPoint: mount, Directory: filepath.Join(mount, relative)}
		}
	}
	if chosen.Directory == "" {
		return chosen, fmt.Errorf("matching cgroup-v2 mount missing")
	}
	return chosen, nil
}

// redactFixedHostTOML follows startup schema sensitivity, preserving switches
// such as token_auth_on. Unknown fields fail closed instead of leaking raw text.
func redactFixedHostTOML(raw []byte) (string, error) {
	var values map[string]any
	if err := toml.Unmarshal(raw, &values); err != nil {
		return "", fmt.Errorf("config TOML cannot be safely decoded")
	}
	leaves := map[string]productconfig.SchemaField{}
	groups := map[string]bool{}
	for _, field := range productconfig.SchemaFields() {
		leaves[field.TOMLPath] = field
		parts := strings.Split(field.TOMLPath, ".")
		for i := 1; i < len(parts); i++ {
			groups[strings.Join(parts[:i], ".")] = true
		}
	}
	var redactNested func(any)
	redactNested = func(value any) {
		switch values := value.(type) {
		case map[string]any:
			for key, nested := range values {
				normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(key))
				sensitive := false
				for _, marker := range []string{"password", "passwd", "secret", "token", "credential", "privatekey", "apikey", "accesskey", "signingkey", "encryptionkey"} {
					sensitive = sensitive || strings.Contains(normalized, marker)
				}
				if sensitive {
					values[key] = "[REDACTED]"
				} else {
					redactNested(nested)
				}
			}
		case []any:
			for _, nested := range values {
				redactNested(nested)
			}
		case []map[string]any:
			for _, nested := range values {
				redactNested(nested)
			}
		}
	}
	var redactGroup func(map[string]any, string) error
	redactGroup = func(group map[string]any, prefix string) error {
		for key, value := range group {
			path := prefix + key
			if field, ok := leaves[path]; ok {
				if field.DiagnosticSensitive {
					group[key] = "[REDACTED]"
				} else if field.Kind == "object_list" || field.Kind == "string_list" {
					if text, encoded := value.(string); encoded && strings.TrimSpace(text) != "" {
						var items []any
						if err := json.Unmarshal([]byte(text), &items); err != nil {
							return fmt.Errorf("config list cannot be safely decoded")
						}
						redactNested(items)
						data, err := json.Marshal(items)
						if err != nil {
							return fmt.Errorf("config list cannot be safely encoded")
						}
						group[key] = string(data)
					} else {
						redactNested(value)
					}
				}
				continue
			}
			nested, ok := value.(map[string]any)
			if !groups[path] || !ok {
				return fmt.Errorf("unknown or invalid config group")
			}
			if err := redactGroup(nested, path+"."); err != nil {
				return err
			}
		}
		return nil
	}
	if err := redactGroup(values, ""); err != nil {
		return "", err
	}
	encoded, err := toml.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("redacted config TOML cannot be encoded")
	}
	return string(encoded), nil
}
