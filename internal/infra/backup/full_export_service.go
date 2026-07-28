package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"golang.org/x/time/rate"
)

// FullExportNode owns current routing, stable metadata capture, and local
// Channel-leader message snapshots.
type FullExportNode interface {
	FullPartitionNode
	NodeID() uint64
	BackupControllerFence(context.Context) (uint64, uint64, error)
	RouteHashSlot(uint16) (clusterpkg.Route, error)
	OpenBackupMessageSnapshot(
		context.Context,
		uint16,
		[]clusterpkg.BackupChannelFence,
	) (clusterpkg.BackupMessageSnapshot, error)
	ValidateBackupHashSlotAuthority(
		context.Context,
		uint16,
		uint32,
		uint64,
		uint64,
	) error
}

// RemoteFullExportClient forwards bounded commands while repository payloads
// remain on their producing data nodes.
type RemoteFullExportClient interface {
	ExportBackupSlot(
		context.Context,
		uint64,
		backupcontract.SlotExportCommand,
	) (backupcontract.SlotExportReceipt, error)
	ExportBackupMessages(
		context.Context,
		uint64,
		backupcontract.MessageExportCommand,
	) (backupcontract.MessageExportReceipt, error)
}

// FullExportService performs node-local Slot and message-stream exports.
type FullExportService struct {
	node       FullExportNode
	repository *RepositoryProvider
	remote     RemoteFullExportClient
	tempDir    string

	limiterMu       sync.Mutex
	limiterBackupID string
	limiterRate     uint64
	limiter         *rate.Limiter
	slotLocks       [backupcontract.HashSlotCount]sync.Mutex
	messageLocks    [backupcontract.HashSlotCount]sync.Mutex
}

// NewFullExportService creates the node-local export endpoint.
func NewFullExportService(
	node FullExportNode,
	repository *RepositoryProvider,
	remote RemoteFullExportClient,
	tempDir string,
) (*FullExportService, error) {
	if node == nil || node.NodeID() == 0 || repository == nil ||
		remote == nil || tempDir == "" {
		return nil, fmt.Errorf("backup full export: dependencies are required")
	}
	absolute, err := filepath.Abs(tempDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o750); err != nil {
		return nil, err
	}
	return &FullExportService{
		node: node, repository: repository, remote: remote, tempDir: absolute,
	}, nil
}

// ExportSlot writes one complete, independently verifiable Hash Slot subtree.
func (s *FullExportService) ExportSlot(
	ctx context.Context,
	command backupcontract.SlotExportCommand,
) (backupcontract.SlotExportReceipt, error) {
	if s == nil || command.OwnerNodeID != s.node.NodeID() ||
		command.OwnerTerm == 0 || command.Attempt == 0 ||
		command.BackupID == "" ||
		int(command.HashSlot) >= backupcontract.HashSlotCount {
		return backupcontract.SlotExportReceipt{},
			fmt.Errorf("backup full export: invalid Slot command")
	}
	slotLock := &s.slotLocks[command.HashSlot]
	slotLock.Lock()
	defer slotLock.Unlock()
	if err := s.validateCoordinator(
		ctx, command.CoordinatorNodeID, command.CoordinatorTerm,
	); err != nil {
		return backupcontract.SlotExportReceipt{}, err
	}
	route, err := s.node.RouteHashSlot(command.HashSlot)
	if err != nil {
		return backupcontract.SlotExportReceipt{}, err
	}
	if route.Leader != command.OwnerNodeID ||
		route.LeaderTerm != command.OwnerTerm ||
		route.ConfigEpoch == 0 || route.SlotID == 0 {
		return backupcontract.SlotExportReceipt{},
			fmt.Errorf("backup full export: stale Slot authority")
	}
	store, err := s.repository.Open(ctx, command.Plan.Store)
	if err != nil {
		return backupcontract.SlotExportReceipt{}, err
	}
	artifactPrefix := fmt.Sprintf(
		"slots/%03d/attempts/%08d-%020d-%020d",
		command.HashSlot, command.Attempt,
		command.OwnerTerm, command.CoordinatorTerm,
	)
	slotRoot := "backups/" + command.BackupID + "/" + artifactPrefix
	if err := store.DeletePrefix(ctx, slotRoot); err != nil {
		return backupcontract.SlotExportReceipt{}, err
	}
	partition, err := OpenFullPartition(
		ctx, s.node, command.HashSlot, route.ConfigEpoch, command.OwnerTerm,
	)
	if err != nil {
		return backupcontract.SlotExportReceipt{}, err
	}
	defer partition.Close()
	if partition.Cut.PhysicalSlotID != route.SlotID ||
		partition.Cut.LeaderTerm != command.OwnerTerm {
		return backupcontract.SlotExportReceipt{},
			fmt.Errorf("backup full export: captured authority changed")
	}
	writer, err := runtimebackup.NewFullStreamWriter(
		runtimebackup.FullStreamWriterOptions{
			Store: store, TempDir: s.tempDir,
		},
	)
	if err != nil {
		return backupcontract.SlotExportReceipt{}, err
	}
	metadataChunks, err := writer.WriteAt(
		ctx, command.BackupID, command.HashSlot, artifactPrefix,
		runtimebackup.FullSlotStream{
			Kind: backupartifact.ChunkKindMetadata,
			Reader: newRateLimitedReadCloser(
				ctx, partition.Metadata,
				s.limiterFor(command.BackupID, command.Plan.RateBytesPerSec),
			),
			Records: partition.MetadataRecords,
		},
		1, 0,
	)
	partition.Metadata = nil
	if err != nil {
		return backupcontract.SlotExportReceipt{}, err
	}
	chunks := append([]backupartifact.ChunkReference(nil), metadataChunks...)
	nextSequence := uint32(1)
	nextStream := uint32(1)
	for _, shard := range partition.MessageShards {
		result, err := s.exportMessages(
			ctx,
			backupcontract.MessageExportCommand{
				Store:             command.Plan.Store,
				BackupID:          command.BackupID,
				HashSlot:          command.HashSlot,
				ArtifactPrefix:    artifactPrefix,
				Shard:             shard,
				FirstSequence:     nextSequence,
				StreamNumber:      nextStream,
				RateBytesPerSec:   command.Plan.RateBytesPerSec,
				CoordinatorNodeID: command.CoordinatorNodeID,
				CoordinatorTerm:   command.CoordinatorTerm,
			},
		)
		if err != nil {
			return backupcontract.SlotExportReceipt{}, err
		}
		messageManifest, err := backupartifact.LoadStoredMessageChunkManifest(
			ctx, store, command.BackupID,
			result.ManifestKey, result.ManifestSHA256,
		)
		if err != nil {
			return backupcontract.SlotExportReceipt{}, err
		}
		if messageManifest.HashSlot != command.HashSlot ||
			uint32(len(messageManifest.Chunks)) != result.ChunkCount ||
			messageManifest.LogicalBytes != result.LogicalBytes ||
			messageManifest.StoredBytes != result.StoredBytes ||
			messageManifest.Records != result.Records ||
			messageManifest.MaxMessageID != result.MaxMessageID ||
			messageManifest.Chunks[0].Sequence != nextSequence ||
			messageManifest.Chunks[0].Stream != nextStream {
			return backupcontract.SlotExportReceipt{},
				fmt.Errorf("backup full export: message receipt mismatch")
		}
		chunks = append(chunks, messageManifest.Chunks...)
		nextSequence += result.ChunkCount
		nextStream++
	}
	if err := s.validateCoordinator(
		ctx, command.CoordinatorNodeID, command.CoordinatorTerm,
	); err != nil {
		return backupcontract.SlotExportReceipt{}, err
	}
	if err := s.node.ValidateBackupHashSlotAuthority(
		ctx, command.HashSlot, route.SlotID,
		command.OwnerTerm, route.ConfigEpoch,
	); err != nil {
		return backupcontract.SlotExportReceipt{}, err
	}
	manifest := backupartifact.SlotManifest{
		Format:   backupartifact.SlotManifestFormat,
		Version:  backupartifact.SlotManifestVersion,
		HashSlot: command.HashSlot,
		Cut:      partition.Cut,
		Chunks:   chunks,
	}
	for _, chunk := range chunks {
		manifest.LogicalBytes += chunk.Descriptor.LogicalBytes
		manifest.StoredBytes += chunk.Descriptor.StoredBytes
		manifest.Records += chunk.Records
		if chunk.MaxMessageID > manifest.MaxMessageID {
			manifest.MaxMessageID = chunk.MaxMessageID
		}
	}
	body, err := backupartifact.MarshalSlotManifest(manifest)
	if err != nil {
		return backupcontract.SlotExportReceipt{}, err
	}
	manifestKey := artifactPrefix + "/manifest.json"
	if err := store.Put(ctx, backupartifact.PutObject{
		Key:           "backups/" + command.BackupID + "/" + manifestKey,
		Body:          bytes.NewReader(body),
		ExpectedBytes: uint64(len(body)),
		IfAbsent:      true,
	}); err != nil {
		return backupcontract.SlotExportReceipt{}, err
	}
	if err := s.validateCoordinator(
		ctx, command.CoordinatorNodeID, command.CoordinatorTerm,
	); err != nil {
		return backupcontract.SlotExportReceipt{}, err
	}
	bodySum := sha256.Sum256(body)
	expectedReference := backupartifact.SlotReference{
		HashSlot: command.HashSlot, ManifestKey: manifestKey,
		ManifestSHA256: hex.EncodeToString(bodySum[:]),
		LogicalBytes:   manifest.LogicalBytes, StoredBytes: manifest.StoredBytes,
		Records: manifest.Records, MaxMessageID: manifest.MaxMessageID,
	}
	reference, storedManifest, err :=
		backupartifact.LoadStoredSlotReference(
			ctx, store, command.BackupID, expectedReference, true,
		)
	if err != nil {
		return backupcontract.SlotExportReceipt{}, err
	}
	if storedManifest.Cut != partition.Cut {
		return backupcontract.SlotExportReceipt{},
			fmt.Errorf("backup full export: stored Slot cut changed")
	}
	if err := s.node.ValidateBackupHashSlotAuthority(
		ctx, command.HashSlot, route.SlotID,
		command.OwnerTerm, route.ConfigEpoch,
	); err != nil {
		return backupcontract.SlotExportReceipt{}, err
	}
	return backupcontract.SlotExportReceipt{
		ManifestKey: reference.ManifestKey, ManifestSHA256: reference.ManifestSHA256,
		LogicalBytes: reference.LogicalBytes, StoredBytes: reference.StoredBytes,
		Records: reference.Records, MaxMessageID: reference.MaxMessageID,
	}, nil
}

// ExportMessages writes one Channel-leader message snapshot directly into the
// shared repository and returns only bounded chunk references.
func (s *FullExportService) ExportMessages(
	ctx context.Context,
	command backupcontract.MessageExportCommand,
) (backupcontract.MessageExportReceipt, error) {
	if s == nil || command.Shard.NodeID != s.node.NodeID() ||
		command.BackupID == "" || command.FirstSequence == 0 ||
		command.StreamNumber == 0 ||
		command.ArtifactPrefix == "" ||
		int(command.HashSlot) >= backupcontract.HashSlotCount ||
		len(command.Shard.Channels) == 0 ||
		len(command.Shard.Channels) > fullBackupChannelsPerShard {
		return backupcontract.MessageExportReceipt{},
			fmt.Errorf("backup full export: invalid message command")
	}
	messageLock := &s.messageLocks[command.HashSlot]
	messageLock.Lock()
	defer messageLock.Unlock()
	if err := s.validateCoordinator(
		ctx, command.CoordinatorNodeID, command.CoordinatorTerm,
	); err != nil {
		return backupcontract.MessageExportReceipt{}, err
	}
	fences := make([]clusterpkg.BackupChannelFence, len(command.Shard.Channels))
	for index, channel := range command.Shard.Channels {
		if channel.LeaderNodeID != s.node.NodeID() {
			return backupcontract.MessageExportReceipt{},
				fmt.Errorf("backup full export: message authority changed")
		}
		fences[index] = clusterpkg.BackupChannelFence{
			ChannelID:           channel.ChannelID,
			ChannelType:         channel.ChannelType,
			LeaderNodeID:        channel.LeaderNodeID,
			ChannelEpoch:        channel.ChannelEpoch,
			LeaderEpoch:         channel.LeaderEpoch,
			MinISR:              channel.MinISR,
			RetentionThroughSeq: channel.RetentionThroughSeq,
		}
	}
	snapshot, err := s.node.OpenBackupMessageSnapshot(
		ctx, command.HashSlot, fences,
	)
	if err != nil {
		return backupcontract.MessageExportReceipt{}, err
	}
	store, err := s.repository.Open(ctx, command.Store)
	if err != nil {
		_ = snapshot.Reader.Close()
		return backupcontract.MessageExportReceipt{}, err
	}
	writer, err := runtimebackup.NewFullStreamWriter(
		runtimebackup.FullStreamWriterOptions{
			Store: store, TempDir: s.tempDir,
		},
	)
	if err != nil {
		_ = snapshot.Reader.Close()
		return backupcontract.MessageExportReceipt{}, err
	}
	chunks, err := writer.WriteAt(
		ctx, command.BackupID, command.HashSlot, command.ArtifactPrefix,
		runtimebackup.FullSlotStream{
			Kind: backupartifact.ChunkKindMessages,
			Reader: newRateLimitedReadCloser(
				ctx, snapshot.Reader,
				s.limiterFor(command.BackupID, command.RateBytesPerSec),
			),
			Records:      snapshot.MessageRecords,
			MaxMessageID: snapshot.MaxMessageID,
		},
		command.FirstSequence, command.StreamNumber,
	)
	if err != nil {
		return backupcontract.MessageExportReceipt{}, err
	}
	if err := s.validateCoordinator(
		ctx, command.CoordinatorNodeID, command.CoordinatorTerm,
	); err != nil {
		return backupcontract.MessageExportReceipt{}, err
	}
	manifest, err := backupartifact.NewMessageChunkManifest(
		command.HashSlot, chunks,
	)
	if err != nil {
		return backupcontract.MessageExportReceipt{}, err
	}
	body, err := backupartifact.MarshalMessageChunkManifest(manifest)
	if err != nil {
		return backupcontract.MessageExportReceipt{}, err
	}
	manifestKey := fmt.Sprintf(
		"%s/message-stream-%06d-manifest.json",
		command.ArtifactPrefix, command.StreamNumber,
	)
	if err := store.Put(ctx, backupartifact.PutObject{
		Key:           "backups/" + command.BackupID + "/" + manifestKey,
		Body:          bytes.NewReader(body),
		ExpectedBytes: uint64(len(body)),
		IfAbsent:      true,
	}); err != nil {
		return backupcontract.MessageExportReceipt{}, err
	}
	if err := s.validateCoordinator(
		ctx, command.CoordinatorNodeID, command.CoordinatorTerm,
	); err != nil {
		return backupcontract.MessageExportReceipt{}, err
	}
	sum := sha256.Sum256(body)
	return backupcontract.MessageExportReceipt{
		ManifestKey: manifestKey, ManifestSHA256: hex.EncodeToString(sum[:]),
		ChunkCount:   uint32(len(chunks)),
		LogicalBytes: manifest.LogicalBytes, StoredBytes: manifest.StoredBytes,
		Records: manifest.Records, MaxMessageID: manifest.MaxMessageID,
	}, nil
}

func (s *FullExportService) validateCoordinator(
	ctx context.Context,
	nodeID uint64,
	term uint64,
) error {
	if s == nil || s.node == nil || nodeID == 0 || term == 0 {
		return fmt.Errorf("backup full export: invalid coordinator fence")
	}
	currentNodeID, currentTerm, err := s.node.BackupControllerFence(ctx)
	if err != nil {
		return err
	}
	if currentNodeID != nodeID || currentTerm != term {
		return fmt.Errorf("backup full export: stale coordinator fence")
	}
	return nil
}

func (s *FullExportService) exportMessages(
	ctx context.Context,
	command backupcontract.MessageExportCommand,
) (backupcontract.MessageExportReceipt, error) {
	if command.Shard.NodeID == s.node.NodeID() {
		return s.ExportMessages(ctx, command)
	}
	return s.remote.ExportBackupMessages(
		ctx, command.Shard.NodeID, command,
	)
}

// DistributedSlotExecutor routes work to the current physical Slot leader.
type DistributedSlotExecutor struct {
	node   FullExportNode
	local  *FullExportService
	remote RemoteFullExportClient
}

// NewDistributedSlotExecutor creates a cluster-semantic Slot executor.
func NewDistributedSlotExecutor(
	node FullExportNode,
	local *FullExportService,
	remote RemoteFullExportClient,
) (*DistributedSlotExecutor, error) {
	if node == nil || local == nil || remote == nil {
		return nil, fmt.Errorf("backup distributed export: dependencies are required")
	}
	return &DistributedSlotExecutor{
		node: node, local: local, remote: remote,
	}, nil
}

// Authority returns the exact current Slot leader fence.
func (e *DistributedSlotExecutor) Authority(
	_ context.Context,
	hashSlot uint16,
) (backupusecase.SlotAuthority, error) {
	route, err := e.node.RouteHashSlot(hashSlot)
	if err != nil {
		return backupusecase.SlotAuthority{}, err
	}
	if route.Leader == 0 || route.LeaderTerm == 0 {
		return backupusecase.SlotAuthority{},
			fmt.Errorf("backup distributed export: Slot leader unavailable")
	}
	return backupusecase.SlotAuthority{
		NodeID: route.Leader, Term: route.LeaderTerm,
	}, nil
}

// ExportSlot executes locally or forwards one bounded command.
func (e *DistributedSlotExecutor) ExportSlot(
	ctx context.Context,
	plan backupcontract.Plan,
	backupID string,
	hashSlot uint16,
	attempt uint32,
	authority backupusecase.SlotAuthority,
) (backupusecase.SlotExportResult, error) {
	coordinatorNodeID, coordinatorTerm, err :=
		e.node.BackupControllerFence(ctx)
	if err != nil {
		return backupusecase.SlotExportResult{}, err
	}
	if coordinatorNodeID != e.node.NodeID() {
		return backupusecase.SlotExportResult{},
			fmt.Errorf("backup distributed export: local node is not coordinator")
	}
	command := backupcontract.SlotExportCommand{
		Plan: plan, BackupID: backupID, HashSlot: hashSlot,
		Attempt:     attempt,
		OwnerNodeID: authority.NodeID, OwnerTerm: authority.Term,
		CoordinatorNodeID: coordinatorNodeID, CoordinatorTerm: coordinatorTerm,
	}
	var receipt backupcontract.SlotExportReceipt
	err = nil
	if authority.NodeID == e.node.NodeID() {
		receipt, err = e.local.ExportSlot(ctx, command)
	} else {
		receipt, err = e.remote.ExportBackupSlot(
			ctx, authority.NodeID, command,
		)
	}
	if err != nil {
		return backupusecase.SlotExportResult{}, err
	}
	return backupusecase.SlotExportResult{
		ManifestKey: receipt.ManifestKey, ManifestSHA256: receipt.ManifestSHA256,
		LogicalBytes: receipt.LogicalBytes, StoredBytes: receipt.StoredBytes,
		Records: receipt.Records, MaxMessageID: receipt.MaxMessageID,
	}, nil
}

type rateLimitedReadCloser struct {
	ctx     context.Context
	reader  io.ReadCloser
	limiter *rate.Limiter
}

func newRateLimitedReadCloser(
	ctx context.Context,
	reader io.ReadCloser,
	limiter *rate.Limiter,
) io.ReadCloser {
	if reader == nil || limiter == nil {
		return reader
	}
	return &rateLimitedReadCloser{
		ctx: ctx, reader: reader, limiter: limiter,
	}
}

// limiterFor returns the one aggregate limiter shared by every export stream
// currently running on this node for the same backup job.
func (s *FullExportService) limiterFor(
	backupID string,
	bytesPerSecond uint64,
) *rate.Limiter {
	if s == nil || backupID == "" || bytesPerSecond == 0 {
		return nil
	}
	s.limiterMu.Lock()
	defer s.limiterMu.Unlock()
	if s.limiter != nil &&
		s.limiterBackupID == backupID &&
		s.limiterRate == bytesPerSecond {
		return s.limiter
	}
	const maximumRateBurst = 64 << 10
	burst := min(bytesPerSecond, uint64(maximumRateBurst))
	s.limiterBackupID = backupID
	s.limiterRate = bytesPerSecond
	s.limiter = rate.NewLimiter(rate.Limit(bytesPerSecond), int(burst))
	return s.limiter
}

func (r *rateLimitedReadCloser) Read(buffer []byte) (int, error) {
	if len(buffer) > r.limiter.Burst() {
		buffer = buffer[:r.limiter.Burst()]
	}
	count, err := r.reader.Read(buffer)
	if count > 0 {
		waitErr := r.limiter.WaitN(r.ctx, count)
		err = errors.Join(err, waitErr)
	}
	return count, err
}

func (r *rateLimitedReadCloser) Close() error {
	return r.reader.Close()
}
