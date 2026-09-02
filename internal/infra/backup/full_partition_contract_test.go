package backup_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestOpenFullPartitionKeepsOnlyTheStableMetadataCutOpen(t *testing.T) {
	node := newFullExportNodeStub(t, 7)
	node.pages = []runtimeMetaPage{{
		items: []metadb.ChannelRuntimeMeta{
			{
				ChannelID: "room-b", ChannelType: 2, Leader: 2,
				ChannelEpoch: 11, LeaderEpoch: 12, MinISR: 1,
			},
			{
				ChannelID: "room-a", ChannelType: 1, Leader: 1,
				ChannelEpoch: 21, LeaderEpoch: 22, MinISR: 2,
			},
		},
		done: true,
	}}

	partition, err := backupinfra.OpenFullPartition(
		context.Background(), node, 7, 31, 9,
	)
	if err != nil {
		t.Fatalf("OpenFullPartition(): %v", err)
	}
	if partition.Cut.PhysicalSlotID != 4 ||
		partition.Cut.LeaderTerm != 9 ||
		partition.Cut.AppliedTerm != 8 ||
		partition.Cut.ConfigurationVersion != 31 ||
		partition.Cut.AppliedIndex != 100 {
		t.Fatalf("cut = %+v", partition.Cut)
	}
	if partition.MetadataRecords == 0 {
		t.Fatal("metadata record count = 0, want the encoded user record")
	}
	if len(partition.MessageShards) != 2 ||
		partition.MessageShards[0].NodeID != 1 ||
		partition.MessageShards[0].Channels[0].ChannelID != "room-a" ||
		partition.MessageShards[1].NodeID != 2 ||
		partition.MessageShards[1].Channels[0].ChannelID != "room-b" {
		t.Fatalf("message shards = %#v", partition.MessageShards)
	}
	if len(node.captureReaders) != 2 ||
		node.captureReaders[0].closeCount != 0 ||
		node.captureReaders[1].closeCount != 1 {
		t.Fatalf("capture close counts before Close = %#v", captureCloseCounts(node))
	}
	if err := partition.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if err := partition.Close(); err != nil {
		t.Fatalf("Close() again: %v", err)
	}
	if node.captureReaders[0].closeCount != 1 ||
		node.captureReaders[1].closeCount != 1 {
		t.Fatalf("capture close counts after Close = %#v", captureCloseCounts(node))
	}
}

func TestOpenFullPartitionRejectsACutThatChangesDuringChannelPlanning(t *testing.T) {
	node := newFullExportNodeStub(t, 7)
	node.captureApplied = []uint64{100, 101}

	partition, err := backupinfra.OpenFullPartition(
		context.Background(), node, 7, 31, 9,
	)
	if err == nil || partition != nil {
		t.Fatalf("OpenFullPartition() = %#v, %v, want changed-cut error", partition, err)
	}
	if len(node.captureReaders) != 2 ||
		node.captureReaders[0].closeCount != 1 ||
		node.captureReaders[1].closeCount != 1 {
		t.Fatalf("capture close counts = %#v", captureCloseCounts(node))
	}
}

func TestOpenFullPartitionRejectsAChannelPageThatCannotAdvance(t *testing.T) {
	node := newFullExportNodeStub(t, 7)
	node.pages = []runtimeMetaPage{{done: false}}

	partition, err := backupinfra.OpenFullPartition(
		context.Background(), node, 7, 31, 9,
	)
	if err == nil || partition != nil {
		t.Fatalf("OpenFullPartition() = %#v, %v, want cursor error", partition, err)
	}
	if len(node.captureReaders) != 1 ||
		node.captureReaders[0].closeCount != 1 {
		t.Fatalf("capture close counts = %#v", captureCloseCounts(node))
	}
}

type runtimeMetaPage struct {
	items []metadb.ChannelRuntimeMeta
	next  metadb.ChannelRuntimeMetaCursor
	done  bool
	err   error
}

type controllerFenceResult struct {
	nodeID uint64
	term   uint64
	err    error
}

type trackedReadCloser struct {
	io.Reader
	closeCount int
}

func (r *trackedReadCloser) Close() error {
	r.closeCount++
	return nil
}

type fullExportNodeStub struct {
	nodeID uint64
	route  clusterpkg.Route

	snapshotBody   []byte
	captureApplied []uint64
	captureReaders []*trackedReadCloser
	captureCalls   int
	captureErr     error

	pages     []runtimeMetaPage
	pageCalls int

	controllerFences []controllerFenceResult
	controllerCalls  int

	messageBody    []byte
	messageRecords uint64
	maxMessageID   uint64
	messageReaders []*trackedReadCloser
	messageFences  [][]clusterpkg.BackupChannelFence
	messageErr     error

	authorityErrors []error
	authorityCalls  int
}

func newFullExportNodeStub(t *testing.T, hashSlot uint16) *fullExportNodeStub {
	t.Helper()
	db, err := metadb.Open(filepath.Join(t.TempDir(), "meta"))
	if err != nil {
		t.Fatalf("meta.Open(): %v", err)
	}
	if err := db.ForHashSlot(hashSlot).CreateUser(
		context.Background(), metadb.User{UID: "backup-user", Token: "token"},
	); err != nil {
		_ = db.Close()
		t.Fatalf("CreateUser(): %v", err)
	}
	reader, err := db.OpenBackupHashSlotSnapshot(
		context.Background(), []uint16{hashSlot},
	)
	if err != nil {
		_ = db.Close()
		t.Fatalf("OpenBackupHashSlotSnapshot(): %v", err)
	}
	body, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	dbErr := db.Close()
	if err := errors.Join(readErr, closeErr, dbErr); err != nil {
		t.Fatalf("read backup metadata snapshot: %v", err)
	}
	return &fullExportNodeStub{
		nodeID: 9,
		route: clusterpkg.Route{
			HashSlot: hashSlot, SlotID: 4, Leader: 9,
			LeaderTerm: 9, ConfigEpoch: 31,
		},
		snapshotBody:   body,
		captureApplied: []uint64{100, 100},
		messageBody:    []byte("portable-message-snapshot"),
		messageRecords: 3,
		maxMessageID:   99,
	}
}

func (s *fullExportNodeStub) NodeID() uint64 { return s.nodeID }

func (s *fullExportNodeStub) BackupControllerFence(
	context.Context,
) (uint64, uint64, error) {
	index := s.controllerCalls
	s.controllerCalls++
	if len(s.controllerFences) == 0 {
		return s.nodeID, 5, nil
	}
	if index >= len(s.controllerFences) {
		index = len(s.controllerFences) - 1
	}
	result := s.controllerFences[index]
	return result.nodeID, result.term, result.err
}

func (s *fullExportNodeStub) RouteHashSlot(uint16) (clusterpkg.Route, error) {
	return s.route, nil
}

func (s *fullExportNodeStub) CaptureBackupHashSlotSnapshot(
	_ context.Context,
	hashSlot uint16,
	expectedLeaderTerm uint64,
) (multiraft.CapturedHashSlotSnapshot, error) {
	if s.captureErr != nil {
		return multiraft.CapturedHashSlotSnapshot{}, s.captureErr
	}
	index := s.captureCalls
	s.captureCalls++
	if index >= len(s.captureApplied) {
		index = len(s.captureApplied) - 1
	}
	applied := s.captureApplied[index]
	reader := &trackedReadCloser{Reader: bytes.NewReader(s.snapshotBody)}
	s.captureReaders = append(s.captureReaders, reader)
	return multiraft.CapturedHashSlotSnapshot{
		SlotID:               multiraft.SlotID(s.route.SlotID),
		HashSlot:             hashSlot,
		AppliedIndex:         applied,
		CommitIndex:          applied,
		AppliedTerm:          8,
		LeaderTerm:           expectedLeaderTerm,
		CapturedAtUnixMillis: 1_800_000_000_000,
		Reader:               reader,
	}, nil
}

func (s *fullExportNodeStub) ListBackupChannelRuntimeMetaPage(
	_ context.Context,
	_ uint16,
	_ metadb.ChannelRuntimeMetaCursor,
	_ int,
) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error) {
	if len(s.pages) == 0 {
		return nil, metadb.ChannelRuntimeMetaCursor{}, true, nil
	}
	index := s.pageCalls
	s.pageCalls++
	if index >= len(s.pages) {
		return nil, metadb.ChannelRuntimeMetaCursor{}, false,
			errors.New("unexpected metadata page request")
	}
	page := s.pages[index]
	return append([]metadb.ChannelRuntimeMeta(nil), page.items...),
		page.next, page.done, page.err
}

func (s *fullExportNodeStub) OpenBackupMessageSnapshot(
	_ context.Context,
	_ uint16,
	fences []clusterpkg.BackupChannelFence,
) (clusterpkg.BackupMessageSnapshot, error) {
	if s.messageErr != nil {
		return clusterpkg.BackupMessageSnapshot{}, s.messageErr
	}
	s.messageFences = append(
		s.messageFences,
		append([]clusterpkg.BackupChannelFence(nil), fences...),
	)
	reader := &trackedReadCloser{Reader: bytes.NewReader(s.messageBody)}
	s.messageReaders = append(s.messageReaders, reader)
	return clusterpkg.BackupMessageSnapshot{
		Reader: reader, MessageRecords: s.messageRecords,
		MaxMessageID: s.maxMessageID,
	}, nil
}

func (s *fullExportNodeStub) ValidateBackupHashSlotAuthority(
	context.Context,
	uint16,
	uint32,
	uint64,
	uint64,
) error {
	index := s.authorityCalls
	s.authorityCalls++
	if index >= len(s.authorityErrors) {
		return nil
	}
	return s.authorityErrors[index]
}

func captureCloseCounts(node *fullExportNodeStub) []int {
	counts := make([]int, len(node.captureReaders))
	for index, reader := range node.captureReaders {
		counts[index] = reader.closeCount
	}
	return counts
}

var _ backupinfra.FullExportNode = (*fullExportNodeStub)(nil)
