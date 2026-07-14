package alibaba

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
)

const maxDiscoveredInstanceTypes = 3

var supportedInstanceTypePattern = regexp.MustCompile(`^ecs\.[cg][0-9]`)

// InstanceTypeCandidate contains only the ECS inventory fields used to build a
// bounded x86, non-GPU spot allowlist for one Infrastructure Preset.
type InstanceTypeCandidate struct {
	// ID is the concrete Alibaba ECS instance type identifier.
	ID string
	// CPUArchitecture is the provider-reported CPU architecture.
	CPUArchitecture string
	// GPUAmount is the number of attached GPUs in the instance type.
	GPUAmount int32
	// FamilyLevel distinguishes enterprise instance families from entry-level families.
	FamilyLevel string
	// PrivateIPv4Capacity is the maximum number of private IPv4 addresses per ENI.
	PrivateIPv4Capacity int32
}

// ConfigDiscoveryAPI is the read-only Alibaba inventory seam used to derive a
// non-secret provider configuration from the currently authenticated account.
type ConfigDiscoveryAPI interface {
	AccountIDHash(context.Context) (string, error)
	EligibleSpotZones(context.Context, string) ([]string, error)
	LatestLinuxImage(context.Context, string) (string, error)
	InstanceTypes(context.Context, string, int32, int32) ([]InstanceTypeCandidate, error)
	AvailableInstanceTypes(context.Context, string, string) (map[string]bool, error)
}

type presetShape struct {
	preset cloudsim.Preset
	cpu    int32
	memory int32
}

var discoveryShapes = []presetShape{
	{preset: cloudsim.PresetSmall, cpu: 2, memory: 4},
	{preset: cloudsim.PresetStandard, cpu: 4, memory: 8},
	{preset: cloudsim.PresetStress, cpu: 8, memory: 16},
}

// DiscoverConfig derives a complete provider configuration from live Alibaba
// inventory without creating any cloud resource.
func DiscoverConfig(ctx context.Context, api ConfigDiscoveryAPI, region string) (Config, error) {
	region = strings.TrimSpace(region)
	if api == nil || region == "" || strings.ContainsAny(region, " \t\r\n") {
		return Config{}, ErrInvalidConfig
	}
	accountIDHash, err := api.AccountIDHash(ctx)
	if err != nil || strings.TrimSpace(accountIDHash) == "" {
		return Config{}, errors.Join(ErrInvalidConfig, err)
	}
	zones, err := api.EligibleSpotZones(ctx, region)
	if err != nil || len(zones) == 0 {
		return Config{}, errors.Join(ErrInvalidConfig, err)
	}
	zones = slices.Clone(zones)
	sort.Strings(zones)
	imageID, err := api.LatestLinuxImage(ctx, region)
	if err != nil || strings.TrimSpace(imageID) == "" {
		return Config{}, errors.Join(ErrInvalidConfig, err)
	}

	candidates := make(map[cloudsim.Preset][]string, len(discoveryShapes))
	for _, shape := range discoveryShapes {
		instanceTypes, listErr := api.InstanceTypes(ctx, region, shape.cpu, shape.memory)
		if listErr != nil {
			return Config{}, errors.Join(ErrInvalidConfig, listErr)
		}
		candidates[shape.preset] = eligibleInstanceTypeIDs(instanceTypes)
		if len(candidates[shape.preset]) == 0 {
			return Config{}, ErrInvalidConfig
		}
	}

	for _, zone := range zones {
		availableTypes, availableErr := api.AvailableInstanceTypes(ctx, region, zone)
		if availableErr != nil {
			return Config{}, errors.Join(ErrInvalidConfig, availableErr)
		}
		presets := make(map[cloudsim.Preset]Preset, len(discoveryShapes))
		complete := true
		for _, shape := range discoveryShapes {
			available := make([]string, 0, maxDiscoveredInstanceTypes)
			for _, instanceType := range candidates[shape.preset] {
				if err := ctx.Err(); err != nil {
					return Config{}, err
				}
				if availableTypes[instanceType] {
					available = append(available, instanceType)
					if len(available) == maxDiscoveredInstanceTypes {
						break
					}
				}
			}
			if len(available) == 0 {
				complete = false
				break
			}
			presets[shape.preset] = Preset{InstanceTypes: available}
		}
		if complete {
			return discoveredConfig(region, zone, imageID, accountIDHash, presets), nil
		}
	}
	return Config{}, ErrInvalidConfig
}

func eligibleInstanceTypeIDs(candidates []InstanceTypeCandidate) []string {
	seen := make(map[string]struct{}, len(candidates))
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		architecture := strings.ToLower(strings.TrimSpace(candidate.CPUArchitecture))
		if architecture != "x86" && architecture != "x86_64" && architecture != "amd64" {
			continue
		}
		if candidate.GPUAmount != 0 || candidate.FamilyLevel == "EntryLevel" || candidate.PrivateIPv4Capacity < 4 ||
			!supportedInstanceTypePattern.MatchString(candidate.ID) {
			continue
		}
		if _, exists := seen[candidate.ID]; exists {
			continue
		}
		seen[candidate.ID] = struct{}{}
		result = append(result, candidate.ID)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(result)))
	return result
}

func discoveredConfig(region, zone, imageID, accountIDHash string, presets map[cloudsim.Preset]Preset) Config {
	return Config{
		Region: region, ZoneID: zone, ImageID: imageID, AccountIDHash: accountIDHash,
		VPCIPv4CIDR: "10.42.0.0/16", VSwitchIPv4CIDR: "10.42.0.0/24",
		SystemDiskCategory: "cloud_essd", SystemDiskSizeGiB: 40,
		DataDiskCategory: "cloud_essd", DataDiskSizeGiB: 100,
		PublicBandwidthMbps: 20,
		PrivateIPv4: map[string]string{
			"node-1": "10.42.0.11", "node-2": "10.42.0.12", "node-3": "10.42.0.13", "sim": "10.42.0.20",
		},
		SimulatorSourceIPv4: []string{"10.42.0.20", "10.42.0.21", "10.42.0.22"},
		Presets:             presets,
	}
}
