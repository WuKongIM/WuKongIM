package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	artifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// PrepareArchive validates the entire published archive, then rebuilds identity
// joins, authority selection and conversion from original rows. It does not
// trust archived selected references as an independently derived result.
func PrepareArchive(ctx context.Context, plan Plan, w Workspace, decoder OriginalDecoder, store artifact.ArchiveStore) (result Preflight, err error) {
	if err := validateHistoryPolicy(plan.History); err != nil {
		return result, err
	}
	if err := validatePluginArtifacts(plan); err != nil {
		return result, err
	}
	if err := validatePluginNodes(plan); err != nil {
		return result, err
	}
	if err := validateMetadataPolicy(plan.Metadata); err != nil {
		return result, err
	}
	b := &captureBatch{ctx: ctx, workspace: w}
	manifest, err := ReadSourceArchive(ctx, store, func(row transfer.SpoolRow) error {
		if bytes.HasPrefix(row.Key, []byte("source/")) || bytes.HasPrefix(row.Key, []byte("plugin-artifacts/")) {
			return b.add(row)
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	if err := b.flush(); err != nil {
		return result, err
	}
	if manifest.Options.PlanDigest != plan.Digest() || manifest.Options.SourceCommit != plan.SourceCommit {
		return result, errors.New("source archive belongs to a different migration plan")
	}
	if len(manifest.Capture.Nodes) != len(plan.Sources) {
		return result, errors.New("source archive node set differs from plan")
	}
	for _, source := range plan.Sources {
		found := false
		for _, node := range manifest.Capture.Nodes {
			if source.NodeID == node.NodeID {
				found = true
			}
		}
		if !found {
			return result, errors.New("source archive node set differs from plan")
		}
	}
	capture := manifest.Capture
	if err := validateCapturedAuthority(capture.Nodes); err != nil {
		return result, err
	}
	capture.Digest = ""
	data, err := json.Marshal(capture)
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != manifest.Capture.Digest {
		return result, errors.New("source archive capture digest mismatch")
	}
	for _, node := range capture.Nodes {
		data, found, err := w.Get(ctx, []byte(fmt.Sprintf("source/%020d/snapshot", node.NodeID)))
		if err != nil {
			return result, err
		}
		var stored NodeSnapshot
		if !found {
			return result, errors.New("source archive node snapshot missing")
		}
		if err := json.Unmarshal(data, &stored); err != nil {
			return result, err
		}
		if !reflect.DeepEqual(stored, node) {
			return result, errors.New("source archive node snapshot mismatch")
		}
	}
	result.PlanDigest = plan.Digest()
	result.SourceCommit = plan.SourceCommit
	result.Capture = manifest.Capture
	if result.PluginSettings, err = PreparePluginSettings(ctx, plan, result.Capture, w, decoder); err != nil {
		return result, err
	}
	if result.PluginArtifacts, err = PreparePluginArtifacts(ctx, plan, result.Capture, w); err != nil {
		return result, err
	}
	w, err = prepareQuarantine(ctx, plan, result.Capture, w, decoder)
	if err != nil {
		return result, err
	}
	decoder, err = certifyEmptyChannels(ctx, result.Capture, w, decoder, plan.Metadata)
	if err != nil {
		return result, err
	}
	result.Catalog, err = BuildSourceCatalog(ctx, result.Capture, w, decoder)
	if err != nil {
		return result, err
	}
	if !reflect.DeepEqual(result.Catalog, manifest.Catalog) {
		return result, errors.New("rebuilt source catalog differs from archive")
	}
	if err = validateQuarantineCatalog(ctx, w); err != nil {
		return result, err
	}
	if err = validateSourceIndexes(ctx, result.Capture, plan.Sources, w, decoder, plan.Metadata, plan.Messages); err != nil {
		return result, err
	}
	result.Selection, err = selectSources(ctx, result.Capture, result.Catalog, w, decoder, plan.Exclusions, plan.Metadata, result.PluginArtifacts, plan.History, plan.Messages)
	if err != nil {
		return result, err
	}
	if !reflect.DeepEqual(result.Selection, manifest.Selection) {
		return result, errors.New("rebuilt source selection differs from archive")
	}
	result.Conversion, err = BuildTargetRecords(ctx, result.Selection, w, decoder)
	if err != nil {
		return result, err
	}
	result.Status = "prepared"
	data, err = json.Marshal(result)
	if err != nil {
		return result, err
	}
	return result, w.Put(ctx, []transfer.SpoolRow{{Key: []byte("workflow/PREPARED"), Value: data}})
}
