package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

const repositoryProbeMaximumBytes = 1024

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
// to observe it and publish a receipt, then reads every receipt locally.
func (p *ClusterRepositoryProbe) ProbeRepository(
	ctx context.Context,
	config backupcontract.StoreConfig,
	store backupartifact.ArchiveStore,
) error {
	if p == nil || store == nil {
		return fmt.Errorf("backup repository probe: unavailable")
	}
	token, err := repositoryProbeToken()
	if err != nil {
		return err
	}
	prefix := "probes/" + token
	defer func() { _ = store.DeletePrefix(context.Background(), prefix) }()
	marker := []byte("wukongim repository probe " + token)
	markerKey := prefix + "/marker"
	if err := store.Put(ctx, backupartifact.PutObject{
		Key: markerKey, Body: bytes.NewReader(marker),
		ExpectedBytes: uint64(len(marker)), IfAbsent: true,
	}); err != nil {
		return err
	}
	markerSum := sha256.Sum256(marker)
	state, err := p.cluster.LocalState(ctx)
	if err != nil {
		return err
	}
	active := make([]uint64, 0, len(state.Nodes))
	for _, node := range state.Nodes {
		if node.JoinState == controller.NodeJoinStateActive &&
			node.HasRole(controller.NodeRoleData) {
			active = append(active, node.NodeID)
		}
	}
	if len(active) == 0 {
		return fmt.Errorf("backup repository probe: no active data nodes")
	}
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
			return fmt.Errorf(
				"backup repository probe: node %d cannot share repository: %w",
				nodeID, err,
			)
		}
		if err := verifyProbeObject(
			ctx, store, command.ReceiptKey, []byte(content), "",
		); err != nil {
			return fmt.Errorf(
				"backup repository probe: node %d receipt is not shared: %w",
				nodeID, err,
			)
		}
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
		return err
	}
	if err := verifyProbeObject(
		ctx, store, command.MarkerKey, nil, command.MarkerSHA256,
	); err != nil {
		return err
	}
	content := []byte(command.ReceiptContent)
	return store.Put(ctx, backupartifact.PutObject{
		Key: command.ReceiptKey, Body: bytes.NewReader(content),
		ExpectedBytes: uint64(len(content)), IfAbsent: true,
	})
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
