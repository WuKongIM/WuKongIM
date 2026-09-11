package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"
)

// AIExampleReceiveProfile names the audited original Linux/amd64 program whose
// Receive replies passed native single-node/three-node tests and two restarts.
// It is not a blanket declaration that arbitrary PDK plugins are compatible.
const AIExampleReceiveProfile = "wk-ai-example-receive-linux-amd64-v1"
const aiExampleProgramSHA256 = "671b3436d1a8d765371077009b1dfd6dec4528a1ce9cdc0dbebe2cfddc5b3224"
const aiExamplePluginNo = "wk.plugin.ai-example"

// PluginCompatibilityEvidence binds the exact checked source registrations.
// Runtime acceptance of the eventual imported cluster remains a separate gate.
type PluginCompatibilityEvidence struct {
	Profile    string   `json:"profile"`
	SourceRows []string `json:"source_row_sha256"`
}

// certifyPluginProfile checks the complete source plugin set. Single-plugin
// scope avoids changing old priority/tie selection semantics. Explicit uniform
// config removes the known source/v3 execution-node-affinity difference.
func certifyPluginProfile(ctx context.Context, p Plan, capture SourceCapture, w Workspace) (*PluginCompatibilityEvidence, error) {
	for _, spec := range p.PluginArtifacts {
		if spec.Profile == SearchPersistRouteProfile {
			return certifySearchProfile(ctx, p, capture, w)
		}
	}
	requested := false
	for _, spec := range p.PluginArtifacts {
		if spec.Profile == "" {
			continue
		}
		requested = true
		if spec.Profile != AIExampleReceiveProfile || spec.PluginNo != aiExamplePluginNo || spec.SHA256 != aiExampleProgramSHA256 || spec.Bytes != 11856443 {
			return nil, errors.New("plugin compatibility profile does not match the audited executable")
		}
	}
	if !requested {
		return nil, nil
	}
	if p.SourceCommit != "a888f89533d0e7d1b2030e06504ca97f1ad891d4" || len(p.PluginArtifacts) != len(p.Sources) || capture.Tables["Plugin"] != uint64(len(p.Sources)) || len(p.PluginConfigs) != 1 || p.PluginConfigs[0].PluginNo != aiExamplePluginNo {
		return nil, errors.New("Receive profile requires one exact plugin per source and explicit uniform config")
	}
	out := &PluginCompatibilityEvidence{Profile: AIExampleReceiveProfile}
	for _, spec := range p.PluginArtifacts {
		if spec.Profile != AIExampleReceiveProfile {
			return nil, errors.New("Receive profile requires every original source executable")
		}
		key := "plugin-settings-original/v2/" + capture.Digest + "/" + p.Digest() + fmt.Sprintf("/%020d/%x", spec.SourceNode, []byte(spec.PluginNo))
		data, found, err := w.Get(ctx, []byte(key))
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, errors.New("Receive profile source registration missing")
		}
		var rec MappedPluginSettings
		if err := UnmarshalState(data, &rec); err != nil {
			return nil, err
		}
		original := rec.Original
		if rec.SourceNode != spec.SourceNode || original.No != aiExamplePluginNo || original.Version != "0.0.1" || original.Priority != 1 || len(original.Methods) != 1 || original.Methods[0] != "Receive" || original.Status > 2 {
			return nil, errors.New("Receive profile original registration is outside its verified contract")
		}
		var config map[string]json.RawMessage
		if !utf8.Valid(original.Config) || json.Unmarshal(original.Config, &config) != nil || len(config) != 1 {
			return nil, errors.New("Receive profile requires its original name-only configuration")
		}
		var name string
		if raw, ok := config["name"]; !ok || len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &name) != nil || len(name) > 65536 {
			return nil, errors.New("Receive profile config.name must be a bounded string")
		}
		out.SourceRows = append(out.SourceRows, rec.SourceRowSHA256)
	}
	sort.Strings(out.SourceRows)
	return out, nil
}

type pluginProfileDecoder struct {
	RecordDecoder
	profile string
	rows    map[string]bool
}

func withPluginProfile(decoder RecordDecoder, evidence *PluginCompatibilityEvidence) RecordDecoder {
	d := pluginProfileDecoder{RecordDecoder: decoder, profile: evidence.Profile, rows: map[string]bool{}}
	for _, hash := range evidence.SourceRows {
		d.rows[hash] = true
	}
	return d
}

func (d pluginProfileDecoder) Describe(row Row, id RecordIdentity) (RecordDescription, error) {
	out, err := d.RecordDecoder.Describe(row, id)
	if err != nil {
		return out, err
	}
	pluginNo := aiExamplePluginNo
	if d.profile == SearchPersistRouteProfile {
		pluginNo = SearchPluginNo
	}
	if row.Table == "PluginUser" && string(row.Fields["PluginNo"]) != pluginNo {
		return out, errors.New("plugin profile cannot preserve another plugin binding")
	}
	if row.Table == "Plugin" && out.Plugin != nil {
		data, err := json.Marshal(row)
		if err != nil {
			return out, err
		}
		if knownPluginProfile(d.profile) && d.rows[diagnosticSHA(data)] {
			out.Plugin.CompatibilityProfile = d.profile
		}
	}
	return out, nil
}
