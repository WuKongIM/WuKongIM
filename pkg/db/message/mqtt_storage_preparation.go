package message

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

const (
	mqttStoragePrepared uint8 = 1
	mqttStorageCanceled uint8 = 2
	mqttStorageConsumed uint8 = 3
)

// MQTTStoragePreparation is capacity evidence, never a Channel durability vote.
type MQTTStoragePreparation struct {
	Nonce              uint64
	Prepared, Canceled bool
	NeedFrom           uint64
}
type mqttPreparedCharge struct{ Position, Bytes uint64 }

// mqttStorageTicket keeps one fenced preparation and cancellation floor per
// Channel. Its exact manifest prevents a funded range from serving other content.
type mqttStorageTicket struct {
	Nonce    uint64
	Phase    uint8
	Manifest DurableProposalManifest
	Charges  []mqttPreparedCharge
}

func mqttStorageTicketKey(key ChannelKey) []byte {
	return encodeMessageSystemPrefix(key, messageSystemIDMQTTStoragePreparation)
}
func loadMQTTStorageTicket(view proposalReadView, key ChannelKey) (mqttStorageTicket, bool, error) {
	var out mqttStorageTicket
	k := mqttStorageTicketKey(key)
	v, found, err := view.Get(k)
	if err != nil || !found {
		return out, found, err
	}
	if len(v) > 32<<10 {
		return out, false, dberrors.ErrCorruptValue
	}
	e, err := rowcodec.UnwrapBorrowed(k, v)
	if err != nil {
		return out, false, err
	}
	if e.Version != 1 || e.Codec != rowcodec.CodecFixed || e.Flags != rowcodec.FlagChecksum {
		return out, false, dberrors.ErrCorruptValue
	}
	d := json.NewDecoder(bytes.NewReader(e.Payload))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil {
		return out, false, dberrors.ErrCorruptValue
	}
	var extra any
	if d.Decode(&extra) != io.EOF || out.Nonce == 0 || out.Phase < mqttStoragePrepared || out.Phase > mqttStorageConsumed || !out.Manifest.StructurallyValid() || len(out.Charges) > 256 || out.Phase != mqttStoragePrepared && len(out.Charges) != 0 {
		return out, false, dberrors.ErrCorruptValue
	}
	var last, total uint64
	for _, c := range out.Charges {
		if c.Position <= last || c.Position <= out.Manifest.BaseOffset || c.Position > out.Manifest.LastOffset || c.Bytes == 0 || c.Bytes > math.MaxUint64-total {
			return out, false, dberrors.ErrCorruptValue
		}
		last = c.Position
		total += c.Bytes
	}
	return out, true, nil
}
func stageMQTTStorageTicket(batch *engine.Batch, key ChannelKey, t mqttStorageTicket) error {
	p, err := json.Marshal(t)
	if err != nil {
		return err
	}
	k := mqttStorageTicketKey(key)
	return batch.Set(k, rowcodec.Wrap(k, 1, rowcodec.CodecFixed, rowcodec.FlagChecksum, p))
}

// MQTTStorageEnabled advertises this optional port only after product wiring.
func (e *Engine) MQTTStorageEnabled() bool { return e != nil && e.db != nil && e.db.mqttStorage != nil }

// PrepareMQTTStorage durably reserves one exact proposal before any original
// mutation is dispatched. Nonce zero allocates the next local leader nonce;
// peers must use that nonce. Cancellation persists its floor before refunding.
func (s *ChannelStore) PrepareMQTTStorage(ctx context.Context, manifest DurableProposalManifest, records []channel.Record, committed, nonce uint64, cancel bool) (MQTTStoragePreparation, error) {
	var out MQTTStoragePreparation

	if ctx == nil || !manifest.StructurallyValid() || manifest.LastOffset-manifest.BaseOffset > 256 || !cancel && (len(records) == 0 || len(records) > 256) || cancel && nonce == 0 {
		return out, channel.ErrInvalidArgument
	}
	if err := s.beginUse(); err != nil {
		return out, err
	}
	defer s.endUse()
	s.log.appendMu.Lock()
	defer s.log.appendMu.Unlock()
	s.log.checkpointMu.Lock()
	defer s.log.checkpointMu.Unlock()
	if err := ctxErr(ctx); err != nil {
		return out, err
	}
	if s.log.db.mqttStorage == nil {
		return out, channel.ErrInvalidArgument
	}
	old, found, err := loadMQTTStorageTicket(s.log.db.engine, s.log.key)
	if err != nil {
		return out, toChannelError(err)
	}
	if cancel {
		return s.cancelMQTTStorageLocked(ctx, manifest, nonce, old, found)
	}
	if found && old.Phase == mqttStoragePrepared && old.Manifest != manifest {
		leo, err := s.log.loadLEOLocked(ctx)
		if err != nil {
			return out, toChannelError(err)
		}
		if leo > old.Manifest.BaseOffset {
			original, present, err := loadDurableProposalFrom(s.log.db.engine, encodeProposalByCommandKey(s.log.key, old.Manifest.CommandID))
			if err != nil {
				return out, toChannelError(err)
			}
			if present {
				if original.manifest != old.Manifest {
					return out, channel.ErrCorruptState
				}
				old.Phase = mqttStorageConsumed
				old.Charges = nil
			} else {
				if _, err = s.cancelMQTTStorageLocked(ctx, old.Manifest, old.Nonce, old, true); err != nil {
					return out, err
				}
				old.Phase = mqttStorageCanceled
				old.Charges = nil
			}
		}
	}
	authorityOrder := 0
	if found {
		authorityOrder = compareMQTTStorageAuthority(manifest, old.Manifest)
	}
	if authorityOrder < 0 {
		return out, channel.ErrCorruptState
	}
	if nonce == 0 {
		if found && old.Manifest == manifest && old.Phase == mqttStoragePrepared {
			nonce = old.Nonce
		} else {
			if authorityOrder > 0 {
				nonce = 1
			} else if old.Nonce == math.MaxUint64 {
				return out, channel.ErrBackpressured
			}
			if authorityOrder <= 0 {
				nonce = old.Nonce + 1
			}
		}
	}
	out.Nonce = nonce
	if found {
		if authorityOrder == 0 && (nonce < old.Nonce || nonce == old.Nonce && (old.Manifest != manifest || old.Phase == mqttStorageCanceled)) {
			return out, channel.ErrCorruptState
		}
		if nonce == old.Nonce && old.Manifest == manifest && old.Phase == mqttStoragePrepared {
			if err = s.log.db.mqttStorage.ensureFunded(ctx); err != nil {
				return out, err
			}
			out.Prepared = true
			return out, nil
		}
		// A fresh authenticated sequencer at the exact same predecessor proves
		// that the previous proposal was abandoned, not an accepted repair tail.
		// Persist its cancellation floor before replacing a missed/unknown prepare.
		if old.Phase == mqttStoragePrepared && old.Manifest != manifest &&
			(authorityOrder > 0 || nonce > old.Nonce) && manifest.BaseOffset == old.Manifest.BaseOffset &&
			manifest.PreviousIndex == old.Manifest.PreviousIndex && manifest.PreviousTerm == old.Manifest.PreviousTerm && manifest.PreviousDigest == old.Manifest.PreviousDigest {
			canceled, err := s.cancelMQTTStorageLocked(ctx, old.Manifest, old.Nonce, old, true)
			if err != nil {
				return out, err
			}
			if canceled.Canceled {
				old.Phase = mqttStorageCanceled
				old.Charges = nil
			}
		}
		if old.Phase == mqttStoragePrepared {
			return out, channel.ErrBackpressured
		}
	}
	mode := AppendStrict
	seen := newAppendValidationSeen(len(records))
	prepared, err := s.prepareExactAppendRecordsLocked(ctx, manifest.BaseOffset, records, manifest, committed, mode, &seen)
	if err != nil {
		if gap, ok := err.(*exactAppendGapError); ok {
			out.NeedFrom = gap.needFrom
		}
		return out, toChannelError(err)
	}
	if prepared.alreadyDurable {
		if err = s.log.db.mqttStorage.ensureFunded(ctx); err != nil {
			return out, err
		}
		out.Prepared = true
		return out, nil
	}
	source, present, err := s.log.mqttStorageSource(prepared.proposals)
	if err != nil {
		return out, toChannelError(err)
	}
	if !present || source.Generation == "" {
		return out, channel.ErrBackpressured
	}
	charges, n, err := s.log.prepareMQTTStorageBooking(ctx, prepared.rows, prepared.proposals)
	if err != nil {
		return out, toChannelError(err)
	}
	ticket := mqttStorageTicket{Nonce: nonce, Phase: mqttStoragePrepared, Manifest: manifest}
	for _, c := range charges {
		ticket.Charges = append(ticket.Charges, mqttPreparedCharge{c.position, c.bytes})
	}
	batch := s.log.db.engine.NewBatch()
	defer batch.Close()
	if err = stageMQTTStorageCharges(batch, s.log.key, charges); err == nil {
		err = stageMQTTStorageTicket(batch, s.log.key, ticket)
	}
	if err != nil {
		s.log.db.mqttStorage.cancelReservation(n)
		return out, toChannelError(err)
	}
	submitted, err := s.log.commitMQTTStoragePreparation(ctx, batch, manifest, nonce, n)
	if !submitted {
		s.log.db.mqttStorage.cancelReservation(n)
	}
	if err != nil {
		return out, toChannelError(err)
	}
	out.Prepared = true
	return out, nil
}

func (s *ChannelStore) cancelMQTTStorageLocked(ctx context.Context, manifest DurableProposalManifest, nonce uint64, old mqttStorageTicket, found bool) (MQTTStoragePreparation, error) {
	out, charges, err := s.planMQTTStorageCancellationLocked(manifest, nonce, old, found)
	if err != nil || out.Canceled || out.Prepared {
		return out, err
	}
	batch := s.log.db.engine.NewBatch()
	defer batch.Close()
	refund, err := stageMQTTStorageCancellation(batch, s.log.key, manifest, nonce, charges)
	if err != nil {
		return out, toChannelError(err)
	}
	_, err = s.log.commitMQTTStorageCancellation(ctx, batch, manifest, nonce, refund)
	if err != nil {
		return out, toChannelError(err)
	}
	out.Canceled = true
	return out, nil
}

// planMQTTStorageCancellationLocked shares exact identity/original checks with
// foreground and managed periodic cancellation. Both Channel locks are held.
func (s *ChannelStore) planMQTTStorageCancellationLocked(manifest DurableProposalManifest, nonce uint64, old mqttStorageTicket, found bool) (MQTTStoragePreparation, []mqttPreparedCharge, error) {
	out := MQTTStoragePreparation{Nonce: nonce}
	if found && compareMQTTStorageAuthority(manifest, old.Manifest) < 0 || found && compareMQTTStorageAuthority(manifest, old.Manifest) == 0 && old.Nonce > nonce {
		out.Canceled = true
		return out, nil, nil
	}
	if found && compareMQTTStorageAuthority(manifest, old.Manifest) == 0 && old.Nonce == nonce && old.Manifest != manifest {
		return out, nil, channel.ErrCorruptState
	}
	if found && old.Phase == mqttStoragePrepared && (old.Manifest != manifest || old.Nonce != nonce) {
		return out, nil, channel.ErrBackpressured
	}
	if found && old.Nonce == nonce && old.Manifest == manifest && old.Phase == mqttStorageConsumed {
		out.Prepared = true
		return out, nil, nil
	}
	// Absence of the original is pinned under the canonical append lock. A later
	// original must find an active exact ticket, so the canceled nonce cannot vote.
	existing, present, err := loadDurableProposalFrom(s.log.db.engine, encodeProposalByCommandKey(s.log.key, manifest.CommandID))
	if err != nil {
		return out, nil, toChannelError(err)
	}
	if present {
		if existing.manifest != manifest {
			return out, nil, channel.ErrCorruptState
		}
		out.Prepared = true
		return out, nil, nil
	}
	if found && old.Nonce == nonce && old.Phase == mqttStoragePrepared {
		return out, old.Charges, nil
	}
	return out, nil, nil
}

func stageMQTTStorageCancellation(batch *engine.Batch, key ChannelKey, manifest DurableProposalManifest, nonce uint64, charges []mqttPreparedCharge) (uint64, error) {
	var refund uint64
	for _, c := range charges {
		if c.Bytes > math.MaxUint64-refund {
			return 0, channel.ErrCorruptState
		}
		refund += c.Bytes
		if err := batch.Delete(mqttStorageChargeKey(key, c.Position)); err != nil {
			return 0, err
		}
	}
	err := stageMQTTStorageTicket(batch, key, mqttStorageTicket{Nonce: nonce, Phase: mqttStorageCanceled, Manifest: manifest})
	return refund, err
}

func (e *channelEntry) stageMQTTStorageConsumption(batch *engine.Batch, proposals []durableProposalRecord) error {
	if e.db.mqttStorage == nil || len(proposals) == 0 {
		return nil
	}
	ticket, found, err := loadMQTTStorageTicket(e.db.engine, e.key)
	if err != nil || !found {
		return err
	}
	for _, p := range proposals {
		if ticket.Phase == mqttStoragePrepared && ticket.Manifest == p.manifest {
			ticket.Phase = mqttStorageConsumed
			ticket.Charges = nil
			return stageMQTTStorageTicket(batch, e.key, ticket)
		}
	}
	return nil
}

// MQTTStorageProtection reads exact local activation evidence, including a
// pending control. It supplies capacity classification, never publication HW.
func (s *ChannelStore) MQTTStorageProtection(ctx context.Context) (bool, error) {
	if err := s.beginUse(); err != nil {
		return false, err
	}
	defer s.endUse()
	if err := ctxErr(ctx); err != nil {
		return false, err
	}
	view, err := s.log.db.engine.NewSnapshot()
	if err != nil {
		return false, toChannelError(err)
	}
	defer view.Close()
	evidence, err := readMQTTActivationEvidence(view, s.log.key)
	if err != nil {
		return false, toChannelError(err)
	}
	return evidence.present || evidence.sourcePresent, nil
}

// compareMQTTStorageAuthority orders nonce domains by the complete write fence.
// A recovered new leader may start a nonce domain only after prior active debt
// is consumed/canceled or its exact original range is canonically occupied.
func compareMQTTStorageAuthority(a, b DurableProposalManifest) int {
	for _, pair := range [][2]uint64{{a.ChannelEpoch, b.ChannelEpoch}, {a.LeaderTerm, b.LeaderTerm}, {a.FenceVersion, b.FenceVersion}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return 0
}
