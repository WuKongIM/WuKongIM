package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

const repositoryProbeMaximumBytes = 1024
const repositoryProbeCleanupTimeout = 15 * time.Second

// RepositoryProbeCluster exposes active data nodes from Controller state.
type RepositoryProbeCluster interface {
	NodeID() uint64
	LocalState(context.Context) (controller.ClusterState, error)
}

// RepositoryProbeRemote forwards one marker observation to another node.
type RepositoryProbeRemote interface {
	ProbeBackupRepository(
		context.Context,
		uint64,
		backupcontract.RepositoryProbeCommand,
	) error
}

// ClusterRepositoryProbe proves that the selected repository is visible from
// every active data node.
type ClusterRepositoryProbe struct {
	cluster    RepositoryProbeCluster
	repository *RepositoryProvider
	remote     RepositoryProbeRemote
}

// NewClusterRepositoryProbe creates the shared-visibility probe.
func NewClusterRepositoryProbe(
	cluster RepositoryProbeCluster,
	repository *RepositoryProvider,
	remote RepositoryProbeRemote,
) (*ClusterRepositoryProbe, error) {
	if cluster == nil || cluster.NodeID() == 0 ||
		repository == nil || remote == nil {
		return nil, fmt.Errorf("backup repository probe: dependencies are required")
	}
	return &ClusterRepositoryProbe{
		cluster: cluster, repository: repository, remote: remote,
	}, nil
}

// ProbeRepository writes one coordinator marker, asks every active data node
// to observe it and publish a receipt, reads every receipt locally, proves the
// expected object set is listable, and confirms bounded cleanup.
func (p *ClusterRepositoryProbe) ProbeRepository(
	ctx context.Context,
	config backupcontract.StoreConfig,
	store backupartifact.ArchiveStore,
) (resultErr error) {
	if p == nil || store == nil {
		return fmt.Errorf("backup repository probe: unavailable")
	}
	token, err := repositoryProbeToken()
	if err != nil {
		return err
	}
	prefix := "probes/" + token
	defer func() {
		cleanupErr := cleanupRepositoryProbe(store, prefix)
		if cleanupErr == nil {
			return
		}
		classified := classifyRepositoryError(
			config.Kind,
			backupcontract.RepositoryAccessDelete,
			cleanupErr,
		)
		if resultErr == nil {
			resultErr = classified
			return
		}
		resultErr = errors.Join(resultErr, classified)
	}()
	marker := []byte("wukongim repository probe " + token)
	markerKey := prefix + "/marker"
	if err := store.Put(ctx, backupartifact.PutObject{
		Key: markerKey, Body: bytes.NewReader(marker),
		ExpectedBytes: uint64(len(marker)), IfAbsent: true,
	}); err != nil {
		return repositoryProbeErrorForNode(
			config.Kind,
			backupcontract.RepositoryAccessWriteMarker,
			p.cluster.NodeID(),
			err,
		)
	}
	markerSum := sha256.Sum256(marker)
	state, err := p.cluster.LocalState(ctx)
	if err != nil {
		return &backupcontract.RepositoryAccessError{
			Reason:   backupcontract.RepositoryAccessNodeUnreachable,
			Stage:    backupcontract.RepositoryAccessReadMarker,
			Provider: config.Kind,
			NodeID:   p.cluster.NodeID(),
			Cause:    err,
		}
	}
	active := make([]uint64, 0, len(state.Nodes))
	for _, node := range state.Nodes {
		if node.JoinState == controller.NodeJoinStateActive &&
			node.HasRole(controller.NodeRoleData) {
			active = append(active, node.NodeID)
		}
	}
	if len(active) == 0 {
		return &backupcontract.RepositoryAccessError{
			Reason:   backupcontract.RepositoryAccessNodeUnreachable,
			Stage:    backupcontract.RepositoryAccessReadMarker,
			Provider: config.Kind,
			Cause:    fmt.Errorf("backup repository probe: no active data nodes"),
		}
	}
	expectedKeys := map[string]struct{}{markerKey: {}}
	for _, nodeID := range active {
		content := strconv.FormatUint(nodeID, 10) + ":" + token
		command := backupcontract.RepositoryProbeCommand{
			Store: config, MarkerKey: markerKey,
			MarkerSHA256:   hex.EncodeToString(markerSum[:]),
			ReceiptKey:     prefix + "/node-" + strconv.FormatUint(nodeID, 10),
			ReceiptContent: content,
		}
		if nodeID == p.cluster.NodeID() {
			err = p.ObserveRepositoryProbe(ctx, command)
		} else {
			err = p.remote.ProbeBackupRepository(ctx, nodeID, command)
		}
		if err != nil {
			return repositoryProbeRemoteError(config.Kind, nodeID, err)
		}
		if err := verifyProbeObject(
			ctx, store, command.ReceiptKey, []byte(content), "",
		); err != nil {
			return repositoryProbeErrorForNode(
				config.Kind,
				backupcontract.RepositoryAccessReadReceipt,
				nodeID,
				err,
			)
		}
		expectedKeys[command.ReceiptKey] = struct{}{}
	}
	objects, err := store.List(ctx, prefix)
	if err != nil {
		return classifyRepositoryError(
			config.Kind,
			backupcontract.RepositoryAccessList,
			err,
		)
	}
	if err := verifyRepositoryProbeList(objects, expectedKeys); err != nil {
		return classifyRepositoryError(
			config.Kind,
			backupcontract.RepositoryAccessList,
			err,
		)
	}
	return nil
}

// ObserveRepositoryProbe verifies the coordinator marker and publishes this
// node's receipt through its independently opened repository client.
func (p *ClusterRepositoryProbe) ObserveRepositoryProbe(
	ctx context.Context,
	command backupcontract.RepositoryProbeCommand,
) error {
	if p == nil || p.repository == nil ||
		command.MarkerKey == "" || command.ReceiptKey == "" ||
		command.ReceiptContent == "" {
		return fmt.Errorf("backup repository probe: invalid command")
	}
	store, err := p.repository.Open(ctx, command.Store)
	if err != nil {
		return classifyRepositoryError(
			command.Store.Kind,
			backupcontract.RepositoryAccessOpen,
			err,
		)
	}
	if err := verifyProbeObject(
		ctx, store, command.MarkerKey, nil, command.MarkerSHA256,
	); err != nil {
		return classifyRepositoryError(
			command.Store.Kind,
			backupcontract.RepositoryAccessReadMarker,
			err,
		)
	}
	content := []byte(command.ReceiptContent)
	return classifyRepositoryError(
		command.Store.Kind,
		backupcontract.RepositoryAccessWriteReceipt,
		store.Put(ctx, backupartifact.PutObject{
			Key: command.ReceiptKey, Body: bytes.NewReader(content),
			ExpectedBytes: uint64(len(content)), IfAbsent: true,
		}),
	)
}

func repositoryProbeErrorForNode(
	provider backupcontract.StoreKind,
	stage backupcontract.RepositoryAccessStage,
	nodeID uint64,
	err error,
) error {
	classified := classifyRepositoryError(provider, stage, err)
	var accessErr *backupcontract.RepositoryAccessError
	if !errors.As(classified, &accessErr) {
		return classified
	}
	clone := *accessErr
	if clone.NodeID == 0 {
		clone.NodeID = nodeID
	}
	return &clone
}

func repositoryProbeRemoteError(
	provider backupcontract.StoreKind,
	nodeID uint64,
	err error,
) error {
	var accessErr *backupcontract.RepositoryAccessError
	if errors.As(err, &accessErr) {
		return repositoryProbeErrorForNode(
			provider,
			backupcontract.RepositoryAccessReadMarker,
			nodeID,
			err,
		)
	}
	return &backupcontract.RepositoryAccessError{
		Reason:   backupcontract.RepositoryAccessNodeUnreachable,
		Stage:    backupcontract.RepositoryAccessReadMarker,
		Provider: provider,
		NodeID:   nodeID,
		Cause:    err,
	}
}

func verifyRepositoryProbeList(
	objects []backupartifact.ArchiveObject,
	expected map[string]struct{},
) error {
	if len(objects) != len(expected) {
		return fmt.Errorf(
			"backup repository probe: listed %d objects, expected %d",
			len(objects),
			len(expected),
		)
	}
	seen := make(map[string]struct{}, len(objects))
	for _, object := range objects {
		if _, ok := expected[object.Key]; !ok {
			return fmt.Errorf(
				"backup repository probe: listed unexpected object",
			)
		}
		if _, duplicate := seen[object.Key]; duplicate {
			return fmt.Errorf(
				"backup repository probe: listed duplicate object",
			)
		}
		seen[object.Key] = struct{}{}
	}
	for key := range expected {
		if _, ok := seen[key]; !ok {
			return fmt.Errorf(
				"backup repository probe: expected object is not listable",
			)
		}
	}
	return nil
}

func cleanupRepositoryProbe(
	store backupartifact.ArchiveStore,
	prefix string,
) error {
	cleanupCtx, cancel := context.WithTimeout(
		context.Background(),
		repositoryProbeCleanupTimeout,
	)
	defer cancel()
	if err := store.DeletePrefix(cleanupCtx, prefix); err != nil {
		return err
	}
	objects, err := store.List(cleanupCtx, prefix)
	if err != nil {
		return err
	}
	if len(objects) != 0 {
		return fmt.Errorf(
			"backup repository probe: cleanup left %d objects",
			len(objects),
		)
	}
	return nil
}

func verifyProbeObject(
	ctx context.Context,
	store backupartifact.ArchiveStore,
	key string,
	expected []byte,
	expectedSHA string,
) error {
	reader, object, err := store.Open(ctx, key)
	if err != nil {
		return err
	}
	defer reader.Close()
	if object.Bytes > repositoryProbeMaximumBytes {
		return fmt.Errorf("probe object is too large")
	}
	body, err := io.ReadAll(io.LimitReader(reader, repositoryProbeMaximumBytes+1))
	if err != nil {
		return err
	}
	if expected != nil && !bytes.Equal(body, expected) {
		return fmt.Errorf("probe content mismatch")
	}
	if expectedSHA != "" {
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != expectedSHA {
			return fmt.Errorf("probe checksum mismatch")
		}
	}
	return nil
}

func repositoryProbeToken() (string, error) {
	body := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, body); err != nil {
		return "", err
	}
	return hex.EncodeToString(body), nil
}
