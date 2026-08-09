package chatlifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

const (
	groupChannelType          uint8 = 2
	maxGroupSetupChannelBatch       = maxGroupCatalogCount

	// MaxGroupSetupSubscribersPerBatch keeps one benchmark request within the
	// downstream subscriber Raft command's fixed UID-count boundary.
	MaxGroupSetupSubscribersPerBatch = 1_000
)

var ErrGroupSetupConfig = errors.New("chat lifecycle group setup: invalid configuration")

var (
	// ErrGroupSetupShapeMismatch rejects a different catalog shape for the
	// already-fenced run before any target mutation.
	ErrGroupSetupShapeMismatch = errors.New("chat lifecycle group setup: catalog shape mismatch")
	// ErrGroupSetupRunConflict enforces one run for one coordinator lifecycle.
	ErrGroupSetupRunConflict = errors.New("chat lifecycle group setup: another run is already fenced")
)

// GroupSetupTarget is the existing black-box benchmark preparation surface.
type GroupSetupTarget interface {
	UpsertChannels(context.Context, model.BatchChannelsRequest) error
	AddSubscribers(context.Context, model.BatchSubscribersRequest) error
}

// GroupSetupOptions fixes the target and both allocation bounds used by setup.
type GroupSetupOptions struct {
	Target                 GroupSetupTarget
	MaxChannelsPerBatch    int
	MaxSubscribersPerBatch int
}

// GroupSetup prepares only the deterministic fixed group catalog.
type GroupSetup struct {
	target                 GroupSetupTarget
	maxChannelsPerBatch    int
	maxSubscribersPerBatch int

	mu          sync.Mutex
	hasRun      bool
	runID       string
	fingerprint [sha256.Size]byte
	complete    bool
	inFlight    chan struct{}
}

// NewGroupSetup validates all setup bounds before target mutation is possible.
func NewGroupSetup(options GroupSetupOptions) (*GroupSetup, error) {
	if options.Target == nil || options.MaxChannelsPerBatch <= 0 ||
		options.MaxChannelsPerBatch > maxGroupSetupChannelBatch ||
		options.MaxSubscribersPerBatch <= 0 ||
		options.MaxSubscribersPerBatch > MaxGroupSetupSubscribersPerBatch {
		return nil, ErrGroupSetupConfig
	}
	return &GroupSetup{
		target:                 options.Target,
		maxChannelsPerBatch:    options.MaxChannelsPerBatch,
		maxSubscribersPerBatch: options.MaxSubscribersPerBatch,
	}, nil
}

// Run reconstructs each group and member only while filling a bounded target batch.
func (s *GroupSetup) Run(ctx context.Context, cfg Config) error {
	if s == nil || s.target == nil || cfg.Validate() != nil {
		return ErrGroupSetupConfig
	}
	identity, err := NewIdentitySpace(cfg.RunID, cfg.Seed, uint64(cfg.Workload.Workers))
	if err != nil {
		return ErrGroupSetupConfig
	}
	catalog, err := NewGroupCatalog(identity, cfg.Workload.Groups)
	if err != nil {
		return ErrGroupSetupConfig
	}
	fingerprint, err := groupSetupFingerprint(cfg, catalog)
	if err != nil {
		return ErrGroupSetupConfig
	}

	for {
		s.mu.Lock()
		if s.hasRun {
			if s.runID != cfg.RunID {
				s.mu.Unlock()
				return ErrGroupSetupRunConflict
			}
			if s.fingerprint != fingerprint {
				s.mu.Unlock()
				return ErrGroupSetupShapeMismatch
			}
			if s.complete {
				s.mu.Unlock()
				return nil
			}
		} else {
			s.hasRun = true
			s.runID = cfg.RunID
			s.fingerprint = fingerprint
		}
		if active := s.inFlight; active != nil {
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-active:
				continue
			}
		}
		active := make(chan struct{})
		s.inFlight = active
		s.mu.Unlock()

		runErr := s.prepareChannels(ctx, cfg.RunID, catalog)
		if runErr == nil {
			runErr = s.prepareSubscribers(ctx, cfg.RunID, catalog)
		}

		s.mu.Lock()
		if runErr == nil {
			s.complete = true
		}
		s.inFlight = nil
		close(active)
		s.mu.Unlock()
		return runErr
	}
}

const groupSetupFingerprintVersion = "wukongim/chat-lifecycle/group-setup-fingerprint/v1"

// These derivation labels make a future ID/member/ownership change an explicit
// setup-shape change even if a small fixture happens to reconstruct the same row.
const (
	groupSetupIdentityDerivation = "identity-namespace/v1+uid-base36/v1"
	groupSetupCatalogDerivation  = "group-id-base36/v1+fixed-member-count/v1"
	groupSetupMemberDerivation   = "catalog-index-plus-member-times-catalog-count/v1"
	groupSetupOwnerDerivation    = "group-index-mod-worker-count/v1"
)

func groupSetupFingerprint(cfg Config, catalog GroupCatalog) ([sha256.Size]byte, error) {
	digest := sha256.New()
	writeGroupSetupString(digest, groupSetupFingerprintVersion)
	writeGroupSetupString(digest, groupSetupIdentityDerivation)
	writeGroupSetupString(digest, groupSetupCatalogDerivation)
	writeGroupSetupString(digest, groupSetupMemberDerivation)
	writeGroupSetupString(digest, groupSetupOwnerDerivation)
	writeGroupSetupString(digest, string(cfg.Profile))
	writeGroupSetupUint64(digest, cfg.Seed)
	writeGroupSetupUint64(digest, uint64(cfg.Workload.Workers))
	writeGroupSetupUint64(digest, uint64(catalog.Count()))
	writeGroupSetupUint64(digest, boolUint64(cfg.Workload.Groups.FixedMembership))
	writeGroupSetupUint64(digest, uint64(cfg.Workload.Groups.Small))
	writeGroupSetupUint64(digest, uint64(cfg.Workload.Groups.Medium))
	writeGroupSetupUint64(digest, uint64(cfg.Workload.Groups.Large))
	writeGroupSetupUint64(digest, uint64(cfg.Workload.Groups.VeryLarge))
	writeGroupSetupUint64(digest, uint64(cfg.Workload.Groups.VeryLargeMembers))
	writeGroupSetupUint64(digest, uint64(cfg.Workload.Groups.VeryLargeSendEvery))
	for index := 0; index < catalog.Count(); index++ {
		group, err := catalog.Group(uint64(index))
		if err != nil {
			return [sha256.Size]byte{}, err
		}
		owner, err := catalog.GroupOwner(uint64(index))
		if err != nil {
			return [sha256.Size]byte{}, err
		}
		writeGroupSetupUint64(digest, group.Index)
		writeGroupSetupString(digest, group.ID)
		writeGroupSetupUint64(digest, uint64(group.Category))
		writeGroupSetupUint64(digest, uint64(group.MemberCount))
		writeGroupSetupUint64(digest, owner)
	}
	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], digest.Sum(nil))
	return fingerprint, nil
}

func writeGroupSetupString(destination hash.Hash, value string) {
	writeGroupSetupUint64(destination, uint64(len(value)))
	_, _ = destination.Write([]byte(value))
}

func writeGroupSetupUint64(destination hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = destination.Write(encoded[:])
}

func boolUint64(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}

func (s *GroupSetup) prepareChannels(ctx context.Context, runID string, catalog GroupCatalog) error {
	for start := 0; start < catalog.Count(); start += s.maxChannelsPerBatch {
		end := min(start+s.maxChannelsPerBatch, catalog.Count())
		channels := make([]model.ChannelItem, 0, end-start)
		for index := start; index < end; index++ {
			group, err := catalog.Group(uint64(index))
			if err != nil {
				return ErrGroupSetupConfig
			}
			channels = append(channels, model.ChannelItem{
				ChannelID: group.ID, ChannelType: groupChannelType,
				Large: group.Category == GroupLarge || group.Category == GroupVeryLarge,
			})
		}
		if err := s.target.UpsertChannels(ctx, model.BatchChannelsRequest{
			RunID: runID, BatchID: groupChannelBatchID(start, end), Upsert: true, Channels: channels,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *GroupSetup) prepareSubscribers(ctx context.Context, runID string, catalog GroupCatalog) error {
	for index := 0; index < catalog.Count(); index++ {
		group, err := catalog.Group(uint64(index))
		if err != nil {
			return ErrGroupSetupConfig
		}
		for start := 0; start < group.MemberCount; start += s.maxSubscribersPerBatch {
			end := min(start+s.maxSubscribersPerBatch, group.MemberCount)
			subscribers := make([]string, 0, end-start)
			for member := start; member < end; member++ {
				uid, memberErr := group.MemberUID(member)
				if memberErr != nil {
					return ErrGroupSetupConfig
				}
				subscribers = append(subscribers, uid)
			}
			if err := s.target.AddSubscribers(ctx, model.BatchSubscribersRequest{
				RunID:   runID,
				BatchID: groupSubscriberBatchID(index, start, end),
				Items: []model.SubscriberItem{{
					ChannelID: group.ID, ChannelType: groupChannelType, Subscribers: subscribers,
				}},
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func groupChannelBatchID(start, end int) string {
	return fmt.Sprintf("chat-lifecycle-groups-v1-%04d-%04d", start, end)
}

func groupSubscriberBatchID(group, start, end int) string {
	return fmt.Sprintf("chat-lifecycle-members-v1-%04d-%06d-%06d", group, start, end)
}
