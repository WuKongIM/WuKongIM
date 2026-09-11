package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"unicode/utf8"
)

// SearchPersistRouteProfile pins the original search executable from plugins
// commit 10b1795449c4dc7da1e8871863f7458c1e94198e for offline mapping.
// Archive-based restart acceptance exposed stale indexes after leader changes;
// cutover additionally requires an upgraded plugin with query-time catch-up.
const SearchPersistRouteProfile = "wk-search-persist-route-linux-amd64-v1"
const searchProgramSHA256 = "68079973350ec42480d84ee83770e5f7910fc06daeba41b746362e28492d939f"

// SearchPluginNo is the exact identity registered by the audited executable.
const SearchPluginNo = "wk.plugin.search"

func knownPluginProfile(profile string) bool {
	return profile == AIExampleReceiveProfile || profile == SearchPersistRouteProfile
}
func matchesPluginProfile(spec PluginArtifactSpec) bool {
	switch spec.Profile {
	case AIExampleReceiveProfile:
		return spec.PluginNo == aiExamplePluginNo && spec.SHA256 == aiExampleProgramSHA256 && spec.Bytes == 11856443
	case SearchPersistRouteProfile:
		return spec.PluginNo == SearchPluginNo && spec.SHA256 == searchProgramSHA256 && spec.Bytes == 63324496
	default:
		return false
	}
}

func certifySearchProfile(ctx context.Context, p Plan, capture SourceCapture, w Workspace) (*PluginCompatibilityEvidence, error) {
	if p.SourceCommit != "a888f89533d0e7d1b2030e06504ca97f1ad891d4" || len(p.PluginArtifacts) != len(p.Sources) || capture.Tables["Plugin"] != uint64(len(p.Sources)) || len(p.PluginConfigs) != 1 || p.PluginConfigs[0].PluginNo != SearchPluginNo {
		return nil, errors.New("search profile requires one exact plugin per source and explicit uniform config")
	}
	out := &PluginCompatibilityEvidence{Profile: SearchPersistRouteProfile}
	seen := map[uint64]bool{}
	for _, spec := range p.PluginArtifacts {
		if spec.Profile != SearchPersistRouteProfile || !matchesPluginProfile(spec) || seen[spec.SourceNode] {
			return nil, errors.New("search profile requires every exact original executable")
		}
		seen[spec.SourceNode] = true
		key := "plugin-settings-original/v2/" + capture.Digest + "/" + p.Digest() + fmt.Sprintf("/%020d/%x", spec.SourceNode, []byte(spec.PluginNo))
		data, found, err := w.Get(ctx, []byte(key))
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, errors.New("search profile source registration missing")
		}
		var rec MappedPluginSettings
		if err := UnmarshalState(data, &rec); err != nil {
			return nil, err
		}
		original := rec.Original
		methods := append([]string(nil), original.Methods...)
		sort.Strings(methods)
		if rec.SourceNode != spec.SourceNode || original.No != SearchPluginNo || original.Version != "0.0.1" || original.Priority != 1 || !slices.Equal(methods, []string{"PersistAfter", "Route"}) || original.Status > 2 {
			return nil, errors.New("search profile registration is outside its verified contract")
		}
		config := bytes.TrimSpace(original.Config)
		if len(config) > 0 {
			var fields map[string]json.RawMessage
			if !utf8.Valid(config) || json.Unmarshal(config, &fields) != nil || len(fields) != 0 {
				return nil, errors.New("search profile requires empty original configuration")
			}
		}
		out.SourceRows = append(out.SourceRows, rec.SourceRowSHA256)
	}
	for _, n := range p.Sources {
		if !seen[n.NodeID] {
			return nil, errors.New("search profile source executable missing")
		}
	}
	sort.Strings(out.SourceRows)
	return out, nil
}
