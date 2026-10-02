package message

import (
	"bytes"
	"context"
	"math"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

var replayTransferBudget = ReadOptions{Limit: 256, MaxBytes: 16 << 20}

type replayTransferFixture struct {
	source, target             *ChannelStore
	sourceEngine, targetEngine *Engine
	targetPath, generation     string
	activation, business       DurableProposalManifest
	all                        MQTTReplayTransfer
}

func newReplayTransferFixture(t *testing.T) *replayTransferFixture {
	t.Helper()
	f := &replayTransferFixture{sourceEngine: openCompatEngine(t), targetPath: t.TempDir()}
	var err error
	f.targetEngine, err = Open(f.targetPath)
	require.NoError(t, err)
	f.source = mustForChannel(t, f.sourceEngine, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	f.target = mustForChannel(t, f.targetEngine, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	t.Cleanup(func() {
		require.NoError(t, f.source.Close())
		require.NoError(t, f.target.Close())
		require.NoError(t, f.targetEngine.Close())
	})
	var control []channel.Record
	f.activation, control = mqttActivationProposal(t, DurableProposalManifest{}, 1)
	var records []channel.Record
	for i := uint64(0); i < 3; i++ {
		r, err := compatibilityRecordFromRow(messageRow{MessageID: 601 + i, ChannelID: "activation", ChannelType: 1,
			FromUID: "alice", ClientMsgNo: string(rune('a' + i)), ServerTimestampMS: 1234,
			FramerFlags: 2, Expire: 60, StreamFlag: 1, StreamNo: "stream", StreamID: 7,
			PublicationMetadata: publicationFixture(t), Payload: []byte("payload")})
		require.NoError(t, err)
		r.Epoch = 1
		records = append(records, r)
	}
	f.business = sealCompatProposalManifest(t, DurableProposalManifest{Version: quorumlog.PublicationProposalManifestVersion,
		ChannelEpoch: 1, LeaderTerm: 2, FenceVersion: 2, CommandID: quorumlog.CommandID{2}, BaseOffset: 1, LastOffset: 4,
		PreviousIndex: 1, PreviousTerm: 1, PreviousDigest: f.activation.Digest}, records)
	for _, s := range []*ChannelStore{f.source, f.target} {
		appendMQTTActivation(t, s, f.activation, control, 1)
		appendMQTTActivation(t, s, f.business, records, 4)
	}
	f.generation = quorumlog.MQTTSourceGeneration(f.activation.CommandID)
	_, err = f.source.log.CopyMQTTReplaySource(context.Background(), f.generation, 1, 4, replayTransferBudget)
	require.NoError(t, err)
	f.all, err = f.source.log.ExportMQTTReplay(context.Background(), f.generation, 1, 4, replayTransferBudget)
	require.NoError(t, err)
	return f
}

func (f *replayTransferFixture) page(t *testing.T, from, through uint64) MQTTReplayTransfer {
	t.Helper()
	p, err := f.source.log.ExportMQTTReplay(context.Background(), f.generation, from, through, replayTransferBudget)
	require.NoError(t, err)
	return p
}

func (f *replayTransferFixture) requireEmpty(t *testing.T) {
	t.Helper()
	_, ok, err := f.target.log.LoadMQTTReplayState(context.Background())
	require.NoError(t, err)
	require.False(t, ok)
	for pos := uint64(1); pos <= 4; pos++ {
		for _, k := range [][]byte{mqttReplayRowKey(f.target.log.key, f.generation, pos), mqttReplayMeterKey(f.target.log.key, f.generation, pos)} {
			_, ok, err := f.targetEngine.engine.Get(k)
			require.NoError(t, err)
			require.False(t, ok, "failed page left partial writes")
		}
	}
}

func TestMQTTReplayTransferPagingRetryTrimAndReopen(t *testing.T) {
	f := newReplayTransferFixture(t)
	ctx := context.Background()
	before, _, err := f.target.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	// The test acts as the external verified-copy decision producer. Import must
	// neither create this decision nor mistake its local result for such proof.
	protected := before
	protected.Revision, protected.CopiedThrough, protected.ReceiptDigest = 2, 4, f.all.After.Digest
	require.NoError(t, f.target.log.ApplyMQTTSourceState(ctx, before.Revision, protected))
	trim, err := f.target.log.TrimPrefixThrough(ctx, 4)
	require.NoError(t, err)
	require.Equal(t, 4, trim.Deleted)
	p1, p2 := f.page(t, 1, 2), f.page(t, 3, 4)
	for _, p := range []MQTTReplayTransfer{p1, p2, p1, f.all} {
		got, err := f.target.log.ImportMQTTReplay(ctx, p.After, p)
		require.NoError(t, err)
		require.Equal(t, p.After, got, "retry reports its accepted page, not later progress")
	}
	got, err := f.target.log.ExportMQTTReplay(ctx, f.generation, 1, 4, replayTransferBudget)
	require.NoError(t, err)
	require.Equal(t, f.all, got)
	measure, err := f.target.log.MeasureMQTTReplayRange(ctx, f.generation, 0, 4)
	require.NoError(t, err)
	require.Equal(t, MQTTReplayMeasure{Messages: 4, Bytes: f.all.After.TotalBytes}, measure)
	current, _, err := f.target.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	require.Equal(t, protected, current)
	for _, r := range f.all.Records {
		_, ok, err := f.targetEngine.engine.Get(encodeMessageRowKey(f.target.log.key, r.Position, 0))
		require.NoError(t, err)
		require.False(t, ok, "import resurrected ordinary history")
	}
	require.NoError(t, f.target.Close())
	require.NoError(t, f.targetEngine.Close())
	f.targetEngine, err = Open(f.targetPath)
	require.NoError(t, err)
	f.target = mustForChannel(t, f.targetEngine, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	got, err = f.target.log.ExportMQTTReplay(ctx, f.generation, 1, 4, replayTransferBudget)
	require.NoError(t, err)
	require.Equal(t, f.all, got)
	clear(got.Records[0].Content)
	got, err = f.target.log.ExportMQTTReplay(ctx, f.generation, 1, 4, ReadOptions{Limit: 1, MaxBytes: 1 << 20})
	require.NoError(t, err)
	require.Len(t, got.Records, 1)
	require.Equal(t, f.all.Records[0], got.Records[0])
	require.Equal(t, uint64(1), got.After.Through)
}

// Resealing a forged page is deliberate: its self-consistent hashes must not
// replace either the caller's independently accepted prefix or local log proof.
func resealReplayTransfer(t *testing.T, key ChannelKey, p *MQTTReplayTransfer) {
	t.Helper()
	s := p.Before
	for i := range p.Records {
		r := &p.Records[i]
		row, err := mqttReplayOriginalRow(key, r.Position, r.Content)
		require.NoError(t, err)
		r.MessageID = row.MessageID
		r.AccountedBytes = uint64(len(row.Payload) + len(row.PublicationMetadata))
		r.TotalBytes, r.TotalStoredBytes = s.TotalBytes+r.AccountedBytes, s.TotalStoredBytes+uint64(len(r.Content))
		r.ContentHash = mqttReplayContentHash(mqttReplayRowKey(key, s.Generation, r.Position), r.Content)
		r.Digest = mqttReplayNextDigest(s.Digest, *r)
		s, err = extendMQTTReplayState(s, *r)
		require.NoError(t, err)
	}
	p.After = s
}

func TestMQTTReplayTransferRejectsForgedContentAndBadLastRecordAtomically(t *testing.T) {
	for _, change := range []string{"payload", "native_fields", "bad_hash", "noncanonical_size", "version", "position", "counter", "expected", "before", "after"} {
		t.Run(change, func(t *testing.T) {
			f := newReplayTransferFixture(t)
			p := f.page(t, 1, 4)
			expected := p.After
			r := &p.Records[len(p.Records)-1]
			switch change {
			case "payload", "native_fields", "noncanonical_size":
				row, err := mqttReplayOriginalRow(f.target.log.key, r.Position, r.Content)
				require.NoError(t, err)
				if change == "payload" {
					row.Payload = []byte("forged!")
					row.PayloadHash = hashPayload(row.Payload)
				} else if change == "native_fields" {
					row.FramerFlags ^= 2
				} else {
					row.PayloadSize++
				}
				r.Content, err = encodeMessageHeader(encodeMessageRowKey(f.target.log.key, r.Position, 0), row)
				require.NoError(t, err)
				resealReplayTransfer(t, f.target.log.key, &p)
				if change != "native_fields" {
					// Even a faulty external semantic decision cannot replace the
					// receiving replica's immutable committed entry proof.
					expected = p.After
				}
			case "bad_hash":
				r.ContentHash[0] ^= 1
			case "version":
				r.ContentVersion++
			case "position":
				r.Position = math.MaxUint64
			case "counter":
				r.TotalStoredBytes = math.MaxUint64
			case "expected":
				expected.Digest[0] ^= 1
			case "before":
				p.Before.TotalBytes = 1
			case "after":
				p.After.Through++
			}
			_, err := f.target.log.ImportMQTTReplay(context.Background(), expected, p)
			require.Error(t, err)
			f.requireEmpty(t)
		})
	}
}

func TestMQTTReplayTransferRequiresCommittedLocalEvidence(t *testing.T) {
	for _, fault := range []string{"activation", "source", "checkpoint", "entry", "command", "last", "activation_command", "wrong_source", "future_hw", "foreign_entry", "broken_link", "tail"} {
		t.Run(fault, func(t *testing.T) {
			f := newReplayTransferFixture(t)
			key := f.target.log.key
			switch fault {
			case "activation":
				deletePhysicalTestKey(t, f.targetEngine, mqttActivationKey(key))
			case "source":
				deletePhysicalTestKey(t, f.targetEngine, mqttSourceKey(key))
			case "checkpoint":
				deletePhysicalTestKey(t, f.targetEngine, encodeCheckpointKey(key))
			case "entry":
				deletePhysicalTestKey(t, f.targetEngine, encodeEntryIdentityKey(key, 3))
			case "command", "activation_command":
				m := f.business
				if fault == "activation_command" {
					m = f.activation
				}
				deletePhysicalTestKey(t, f.targetEngine, encodeProposalByCommandKey(key, m.CommandID))
			case "last":
				deletePhysicalTestKey(t, f.targetEngine, encodeProposalByLastKey(key, f.business.LastOffset))
			case "wrong_source":
				s := MQTTSourceState{Generation: "foreign", Revision: 1}
				setPhysicalTestValue(t, f.targetEngine, mqttSourceKey(key), encodeMQTTSourceState(mqttSourceKey(key), s))
			case "future_hw":
				setPhysicalTestValue(t, f.targetEngine, encodeCheckpointKey(key), encodeCheckpoint(Checkpoint{HW: 2}))
			case "foreign_entry", "broken_link", "tail":
				pos := uint64(3)
				if fault == "tail" {
					pos = 4
				}
				e, ok, err := loadDurableEntryIdentityFrom(f.targetEngine.engine, key, pos)
				require.NoError(t, err)
				require.True(t, ok)
				if fault == "foreign_entry" {
					e.CommandID[0]++
				} else if fault == "broken_link" {
					e.PreviousDigest[0]++
				} else {
					e.Digest[0]++
				}
				setPhysicalTestValue(t, f.targetEngine, encodeEntryIdentityKey(key, pos), encodeDurableEntryIdentity(e))
			}
			_, err := f.target.log.ImportMQTTReplay(context.Background(), f.all.After, f.all)
			require.Error(t, err)
			f.requireEmpty(t)
		})
	}
}

func TestMQTTReplayTransferRejectsGapsOverlapAndCorruptRetry(t *testing.T) {
	f := newReplayTransferFixture(t)
	ctx := context.Background()
	gap := f.page(t, 2, 4)
	_, err := f.target.log.ImportMQTTReplay(ctx, gap.After, gap)
	require.ErrorIs(t, err, dberrors.ErrConflict)
	f.requireEmpty(t)
	first := f.page(t, 1, 2)
	_, err = f.target.log.ImportMQTTReplay(ctx, first.After, first)
	require.NoError(t, err)
	_, err = f.target.log.ImportMQTTReplay(ctx, f.all.After, f.all)
	require.ErrorIs(t, err, dberrors.ErrConflict)
	state, _, err := f.target.log.LoadMQTTReplayState(ctx)
	require.NoError(t, err)
	require.Equal(t, first.After, state)
	deletePhysicalTestKey(t, f.targetEngine, mqttReplayMeterKey(f.target.log.key, f.generation, 1))
	_, err = f.target.log.ImportMQTTReplay(ctx, first.After, first)
	require.Error(t, err, "a retry must not silently repair a missing meter")
}

func TestMQTTReplayTransferRejectsOrphans(t *testing.T) {
	for _, meter := range []bool{false, true} {
		t.Run(map[bool]string{false: "row", true: "meter"}[meter], func(t *testing.T) {
			f := newReplayTransferFixture(t)
			key := mqttReplayRowKey(f.target.log.key, f.generation, 4)
			if meter {
				key = mqttReplayMeterKey(f.target.log.key, f.generation, 4)
			}
			setPhysicalTestValue(t, f.targetEngine, key, []byte("orphan"))
			_, err := f.target.log.ImportMQTTReplay(context.Background(), f.all.After, f.all)
			require.Error(t, err)
			_, present, err := f.target.log.LoadMQTTReplayState(context.Background())
			require.NoError(t, err)
			require.False(t, present)
			got, present, err := f.targetEngine.engine.Get(key)
			require.NoError(t, err)
			require.True(t, present)
			require.Equal(t, []byte("orphan"), got)
		})
	}
}

func TestMQTTReplayTransferBoundsCancellationAndWrongChannel(t *testing.T) {
	f := newReplayTransferFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.target.log.ImportMQTTReplay(ctx, f.all.After, f.all)
	require.ErrorIs(t, err, context.Canceled)
	_, err = f.source.log.ExportMQTTReplay(ctx, f.generation, 1, 4, replayTransferBudget)
	require.ErrorIs(t, err, context.Canceled)
	for _, opts := range []ReadOptions{{}, {Limit: 257, MaxBytes: 1024}, {Limit: 1, MaxBytes: mqttReplayMaxBytes + 1}, {Limit: 1, MaxBytes: 1}} {
		_, err := f.source.log.ExportMQTTReplay(context.Background(), f.generation, 1, 4, opts)
		require.Error(t, err)
	}
	p, err := f.source.log.ExportMQTTReplay(context.Background(), f.generation, 1, 4, ReadOptions{Limit: 256, MaxBytes: len(f.all.Records[0].Content)})
	require.NoError(t, err)
	require.Len(t, p.Records, 1)
	for _, mutation := range []string{"empty", "rows", "bytes", "generation", "start", "overflow", "channel"} {
		p := f.page(t, 1, 4)
		switch mutation {
		case "empty":
			p.Records = nil
		case "rows":
			p.Records = make([]MQTTReplayRecord, 257)
		case "bytes":
			p.Records[0].Content = make([]byte, mqttReplayMaxBytes+1)
		case "generation":
			p.Before.Generation, p.After.Generation = "other", "other"
		case "start":
			p.Before.StartAfter, p.After.StartAfter = 1, 1
		case "overflow":
			p.Before.Through, p.After.Through = math.MaxUint64, math.MaxUint64
		case "channel":
			row, err := mqttReplayOriginalRow(f.target.log.key, 1, p.Records[0].Content)
			require.NoError(t, err)
			p.Records[0].Content, err = encodeMessageHeader(encodeMessageRowKey(ChannelKey("other:1"), 1, 0), row)
			require.NoError(t, err)
		}
		_, err = f.target.log.ImportMQTTReplay(context.Background(), p.After, p)
		require.Error(t, err, mutation)
	}
	f.requireEmpty(t)
	// Distinct export calls must own independent bytes.
	p = f.page(t, 1, 4)
	require.False(t, bytes.Equal(p.Records[0].Content, nil))
	clear(p.Records[0].Content)
	require.Equal(t, f.all, f.page(t, 1, 4))
}
