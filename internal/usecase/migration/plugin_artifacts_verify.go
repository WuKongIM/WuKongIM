package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// NativePluginArtifact describes bytes read from a regular native executable.
// Mode must be exactly 0500, the offline import's native executable permission.
type NativePluginArtifact struct {
	PluginNo string `json:"plugin_no"`
	Bytes    int64  `json:"bytes"`
	SHA256   string `json:"sha256"`
	Mode     uint32 `json:"mode"`
}

type PluginArtifactView interface {
	WalkPluginArtifacts(context.Context, func(NativePluginArtifact) error) error
}

type PluginArtifactsVerification struct {
	PlanDigest string            `json:"plan_digest"`
	ByTarget   map[uint64]uint64 `json:"files_by_target"`
	Digest     string            `json:"digest"`
}

// VerifyPluginArtifacts derives native expectations from original plan entries
// and rehashed archived bytes. It never consumes importer assignment records.
func VerifyPluginArtifacts(ctx context.Context, p Plan, w Workspace, inspector TargetInspector) (out PluginArtifactsVerification, err error) {
	if err := validatePluginArtifacts(p); err != nil {
		return out, err
	}
	if err := validatePluginNodes(p); err != nil {
		return out, err
	}
	for _, spec := range p.PluginArtifacts {
		if _, err := WalkPluginArtifact(ctx, w, spec, nil); err != nil {
			return out, err
		}
	}
	out = PluginArtifactsVerification{PlanDigest: p.Digest(), ByTarget: map[uint64]uint64{}}
	h := sha256.New()
	enc := json.NewEncoder(h)
	if err := enc.Encode(out.PlanDigest); err != nil {
		return out, err
	}
	for _, node := range p.Target.Nodes {
		want := map[string]PluginArtifactSpec{}
		for _, a := range p.PluginNodes {
			if a.TargetNode != node.NodeID {
				continue
			}
			for _, spec := range p.PluginArtifacts {
				if spec.SourceNode == a.SourceNode {
					want[spec.PluginNo] = spec
				}
			}
		}
		view, err := inspector.Open(ctx, p.Target, node)
		if err != nil {
			return out, err
		}
		artifacts, ok := view.(PluginArtifactView)
		if !ok {
			return out, errors.Join(errors.New("target inspector lacks plugin executable verification"), view.Close())
		}
		out.ByTarget[node.NodeID] = 0
		searchExpected := false
		for _, spec := range want {
			searchExpected = searchExpected || spec.Profile == SearchPersistRouteProfile
		}
		err = artifacts.WalkPluginArtifacts(ctx, func(got NativePluginArtifact) error {
			expected, found := want[got.PluginNo]
			if !found || got.Bytes != expected.Bytes || got.SHA256 != expected.SHA256 || got.Mode != 0500 {
				return errors.New("native plugin executable differs from original plan")
			}
			delete(want, got.PluginNo)
			out.ByTarget[node.NodeID]++
			return enc.Encode(struct {
				NodeID uint64
				File   NativePluginArtifact
			}{node.NodeID, got})
		})
		if err == nil && searchExpected {
			search, ok := view.(SearchCheckpointView)
			if !ok {
				err = errors.New("target inspector lacks search checkpoint verification")
			} else {
				err = VerifySearchCheckpoints(ctx, w, search)
			}
		}
		if err := errors.Join(err, view.Close()); err != nil {
			return out, fmt.Errorf("target node %d plugin executable: %w", node.NodeID, err)
		}
		if len(want) != 0 {
			return out, errors.New("native plugin executable is missing")
		}
	}
	out.Digest = hex.EncodeToString(h.Sum(nil))
	return out, nil
}
