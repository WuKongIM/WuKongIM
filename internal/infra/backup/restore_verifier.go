package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

const maxRestoreVerifyBoundariesPerRequest = 4096

// RestoreVerificationClusterNode exposes local membership and verification.
type RestoreVerificationClusterNode interface {
	NodeID() uint64
	LocalControlSnapshot(context.Context) (control.Snapshot, error)
	VerifyLocalRestorePartition(context.Context, uint16, string, []clusterpkg.RestoreVerifyBoundary) error
}

// RemoteRestoreVerificationClient verifies one bounded batch on another node.
type RemoteRestoreVerificationClient interface {
	VerifyBackupRestorePartition(context.Context, uint64, uint16, string, []clusterpkg.RestoreVerifyBoundary) error
}

// ClusterRestoreVerifierOptions configures authenticated post-install verification.
type ClusterRestoreVerifierOptions struct {
	Primary     backupartifact.Repository
	Secondary   backupartifact.Repository
	Signer      backupartifact.ManifestSigner
	Codec       *backupartifact.ObjectCodec
	Node        RestoreVerificationClusterNode
	Remote      RemoteRestoreVerificationClient
	MaxParallel int
}

// ClusterRestoreVerifier authenticates the selected restore point and proves
// every expected durable Channel cut on every current target node.
type ClusterRestoreVerifier struct {
	primary     backupartifact.Repository
	secondary   backupartifact.Repository
	signer      backupartifact.ManifestSigner
	codec       *backupartifact.ObjectCodec
	node        RestoreVerificationClusterNode
	remote      RemoteRestoreVerificationClient
	maxParallel int
}

// NewClusterRestoreVerifier creates a fail-closed restore verifier.
func NewClusterRestoreVerifier(options ClusterRestoreVerifierOptions) (*ClusterRestoreVerifier, error) {
	if options.Primary == nil || options.Secondary == nil || options.Signer == nil || options.Codec == nil || options.Node == nil || options.Remote == nil ||
		options.Node.NodeID() == 0 || options.MaxParallel <= 0 || options.Primary.Name() == options.Secondary.Name() {
		return nil, fmt.Errorf("backup cluster restore verifier: invalid options")
	}
	return &ClusterRestoreVerifier{
		primary: options.Primary, secondary: options.Secondary, signer: options.Signer, codec: options.Codec,
		node: options.Node, remote: options.Remote, maxParallel: options.MaxParallel,
	}, nil
}

// VerifyRestore returns ordered verified reports only after every current node
// proves the authenticated expected cuts for every logical hash slot.
func (v *ClusterRestoreVerifier) VerifyRestore(ctx context.Context, plan backupusecase.RestorePlan) ([]backupusecase.RestorePartition, error) {
	if v == nil || plan.ID == "" || plan.RestorePointID == "" || plan.HashSlotCount == 0 || len(plan.Partitions) != int(plan.HashSlotCount) ||
		(plan.Repository != "primary" && plan.Repository != "secondary") {
		return nil, backupusecase.ErrInvalidRequest
	}
	repository := v.primary
	if plan.Repository == "secondary" {
		repository = v.secondary
	}
	manifest, err := backupartifact.LoadRestorePoint(ctx, repository, plan.RestorePointID, v.signer)
	if err != nil {
		return nil, err
	}
	body, err := backupartifact.MarshalManifest(manifest)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(body)
	if hex.EncodeToString(hash[:]) != plan.ManifestSHA256 || manifest.SourceClusterID != plan.SourceClusterID ||
		manifest.SourceGeneration != plan.SourceGeneration || manifest.HashSlotCount != plan.HashSlotCount || len(manifest.Partitions) != int(plan.HashSlotCount) {
		return nil, fmt.Errorf("%w: restore verification manifest fence mismatch", backupartifact.ErrInvalidManifest)
	}
	nodeIDs, err := v.currentNodeIDs(ctx, plan)
	if err != nil {
		return nil, err
	}

	type result struct {
		hashSlot uint16
		err      error
	}
	work := make(chan uint16)
	results := make(chan result, plan.HashSlotCount)
	workers := v.maxParallel
	if workers > int(plan.HashSlotCount) {
		workers = int(plan.HashSlotCount)
	}
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for hashSlot := range work {
				reference := manifest.Partitions[hashSlot]
				boundaries, err := v.loadExpectedBoundaries(ctx, repository, reference)
				if err == nil {
					report := plan.Partitions[hashSlot]
					if report.HashSlot != hashSlot || report.EvidenceVersion != reference.Evidence.Version || !report.Installed || !validLowerSHA256(report.MetadataSHA256) ||
						report.MetadataRecordCount != reference.Evidence.MetadataRecords || report.MessageCount != reference.Evidence.MessageRecords ||
						report.MaxMessageID != reference.Evidence.MaxMessageID {
						err = backupusecase.ErrStateConflict
					} else {
						err = v.verifyPartitionOnNodes(ctx, nodeIDs, hashSlot, report.MetadataSHA256, boundaries)
					}
				}
				results <- result{hashSlot: hashSlot, err: err}
			}
		}()
	}
	go func() {
		defer close(work)
		for hashSlot := uint16(0); hashSlot < plan.HashSlotCount; hashSlot++ {
			select {
			case work <- hashSlot:
			case <-ctx.Done():
				return
			}
		}
	}()
	group.Wait()
	close(results)
	for item := range results {
		if item.err != nil {
			return nil, fmt.Errorf("backup cluster restore verifier: hash slot %d: %w", item.hashSlot, item.err)
		}
	}
	reports := append([]backupusecase.RestorePartition(nil), plan.Partitions...)
	for index := range reports {
		if reports[index].HashSlot != uint16(index) || !reports[index].Installed {
			return nil, backupusecase.ErrStateConflict
		}
		reports[index].Verified = true
		reports[index].FailureCategory = ""
	}
	return reports, nil
}

func (v *ClusterRestoreVerifier) currentNodeIDs(ctx context.Context, plan backupusecase.RestorePlan) ([]uint64, error) {
	snapshot, err := v.node.LocalControlSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	if snapshot.ClusterID != plan.TargetClusterID || snapshot.HashSlots.Count != plan.HashSlotCount {
		return nil, fmt.Errorf("backup cluster restore verifier: target topology fence mismatch")
	}
	seen := make(map[uint64]struct{}, len(snapshot.Nodes))
	nodeIDs := make([]uint64, 0, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if node.JoinState == control.NodeJoinStateRemoved {
			continue
		}
		if node.NodeID == 0 {
			return nil, fmt.Errorf("backup cluster restore verifier: invalid current node")
		}
		if _, exists := seen[node.NodeID]; exists {
			return nil, fmt.Errorf("backup cluster restore verifier: duplicate current node")
		}
		seen[node.NodeID] = struct{}{}
		nodeIDs = append(nodeIDs, node.NodeID)
	}
	if len(nodeIDs) == 0 || len(nodeIDs) > maxRestoreInstallNodes {
		return nil, fmt.Errorf("backup cluster restore verifier: current node count is invalid")
	}
	if _, exists := seen[v.node.NodeID()]; !exists {
		return nil, fmt.Errorf("backup cluster restore verifier: local node is outside membership")
	}
	sort.Slice(nodeIDs, func(left, right int) bool { return nodeIDs[left] < nodeIDs[right] })
	return nodeIDs, nil
}

func (v *ClusterRestoreVerifier) loadExpectedBoundaries(ctx context.Context, repository backupartifact.Repository, reference backupartifact.PartitionReference) ([]clusterpkg.RestoreVerifyBoundary, error) {
	layers, err := loadRestorePartitionLayers(ctx, repository, reference)
	if err != nil {
		return nil, err
	}
	groups, err := restoreObjectGroups(layers[len(layers)-1].Objects, backupartifact.ObjectKindChannelIndex)
	if err != nil || len(groups) != 1 || groups[0].Name != string(backupartifact.ObjectKindChannelIndex) {
		return nil, fmt.Errorf("%w: restore channel index stream is invalid", backupartifact.ErrInvalidManifest)
	}
	var plaintext bytes.Buffer
	for _, object := range groups[0].Objects {
		if object.PlaintextBytes < 0 || int64(plaintext.Len())+object.PlaintextBytes > maxLoadedChannelIndexBytes {
			return nil, fmt.Errorf("%w: restore channel index exceeds limit", backupartifact.ErrInvalidManifest)
		}
		ciphertext, err := readRepositoryObject(ctx, repository, object.Key, object.CiphertextBytes, object.CiphertextSHA256)
		if err != nil {
			return nil, err
		}
		chunk, err := v.codec.Open(ctx, object, ciphertext)
		if err != nil {
			return nil, err
		}
		_, _ = plaintext.Write(chunk)
	}
	hashSlot, boundaries, err := backupartifact.LoadChannelIndex(plaintext.Bytes())
	if err != nil {
		return nil, err
	}
	if hashSlot != reference.HashSlot {
		return nil, fmt.Errorf("%w: restore channel index hash slot mismatch", backupartifact.ErrInvalidManifest)
	}
	result := make([]clusterpkg.RestoreVerifyBoundary, len(boundaries))
	for index, boundary := range boundaries {
		result[index] = clusterpkg.RestoreVerifyBoundary{
			ChannelID: boundary.ChannelID, ChannelType: boundary.ChannelType, Epoch: boundary.Epoch,
			LogStartOffset: boundary.LogStartOffset, HW: boundary.HW,
		}
	}
	return result, nil
}

func (v *ClusterRestoreVerifier) verifyPartitionOnNodes(ctx context.Context, nodeIDs []uint64, hashSlot uint16, metadataSHA256 string, boundaries []clusterpkg.RestoreVerifyBoundary) error {
	for _, nodeID := range nodeIDs {
		if len(boundaries) == 0 {
			if err := v.verifyNodeBatch(ctx, nodeID, hashSlot, metadataSHA256, nil); err != nil {
				return err
			}
			continue
		}
		for start := 0; start < len(boundaries); start += maxRestoreVerifyBoundariesPerRequest {
			end := start + maxRestoreVerifyBoundariesPerRequest
			if end > len(boundaries) {
				end = len(boundaries)
			}
			digest := ""
			if start == 0 {
				digest = metadataSHA256
			}
			if err := v.verifyNodeBatch(ctx, nodeID, hashSlot, digest, boundaries[start:end]); err != nil {
				return fmt.Errorf("node %d: %w", nodeID, err)
			}
		}
	}
	return nil
}

func (v *ClusterRestoreVerifier) verifyNodeBatch(ctx context.Context, nodeID uint64, hashSlot uint16, metadataSHA256 string, boundaries []clusterpkg.RestoreVerifyBoundary) error {
	if nodeID == v.node.NodeID() {
		return v.node.VerifyLocalRestorePartition(ctx, hashSlot, metadataSHA256, boundaries)
	}
	return v.remote.VerifyBackupRestorePartition(ctx, nodeID, hashSlot, metadataSHA256, boundaries)
}

var _ backupusecase.RestoreFinalVerifier = (*ClusterRestoreVerifier)(nil)
