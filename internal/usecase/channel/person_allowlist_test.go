package channel

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

// Exercise the failure boundaries before implementing the personal allowlist workflow:
// missing capabilities/invalid identity/cancellation must precede ACL writes;
// ACL failure prevents preparation; preparation failure prevents activation;
// activation failure remains retryable even when the ACL already contains the peer.
func TestPersonalAllowlistFailureBoundaries(t *testing.T) {
	wantErr := errors.New("authority unavailable")
	for _, stage := range []string{"acl", "directory", "activation"} {
		t.Run(stage, func(t *testing.T) {
			state := &personalAllowlistPorts{failStage: stage, failErr: wantErr}
			app := New(Options{Store: state, PersonDirectory: state, MembershipActivation: state})
			cmd := MemberCommand{ChannelKey: ChannelKey{ChannelID: "owner", ChannelType: 1}, UIDs: []string{"peer"}}
			if err := app.AddAllowlist(context.Background(), cmd); !errors.Is(err, wantErr) {
				t.Fatalf("AddAllowlist() = %v, want %v", err, wantErr)
			}
			wantCalls := []string{"acl"}
			if stage != "acl" {
				wantCalls = append(wantCalls, "directory")
			}
			if stage == "activation" {
				wantCalls = append(wantCalls, "activation")
			}
			if !reflect.DeepEqual(state.calls, wantCalls) {
				t.Fatalf("calls = %v, want %v", state.calls, wantCalls)
			}
			state.calls, state.failStage = nil, ""
			if err := app.AddAllowlist(context.Background(), cmd); err != nil {
				t.Fatalf("retry: %v", err)
			}
			if !reflect.DeepEqual(state.calls, []string{"acl", "directory", "activation"}) {
				t.Fatalf("retry did not finish the complete workflow: %v", state.calls)
			}
		})
	}
}

func TestPersonalAllowlistRejectsIncompletePortsBeforeMutation(t *testing.T) {
	for _, missing := range []string{"directory", "activation"} {
		t.Run(missing, func(t *testing.T) {
			state := &personalAllowlistPorts{}
			opts := Options{Store: state, PersonDirectory: state, MembershipActivation: state}
			if missing == "directory" {
				opts.PersonDirectory = nil
			} else {
				opts.MembershipActivation = nil
			}
			err := New(opts).AddAllowlist(context.Background(), MemberCommand{ChannelKey: ChannelKey{ChannelID: "owner", ChannelType: 1}, UIDs: []string{"peer"}})
			if !errors.Is(err, ErrStoreRequired) || len(state.calls) != 0 {
				t.Fatalf("error/calls = %v/%v, want missing-port error without mutations", err, state.calls)
			}
		})
	}
}

func TestPersonalAllowlistValidatesWholeInputBeforeMutation(t *testing.T) {
	for _, cmd := range []MemberCommand{
		{ChannelKey: ChannelKey{ChannelID: "owner", ChannelType: 1}, UIDs: []string{"valid", "invalid@peer"}},
		{ChannelKey: ChannelKey{ChannelID: "owner", ChannelType: 1}, UIDs: []string{"valid", " "}},
		{ChannelKey: ChannelKey{ChannelID: "owner@peer", ChannelType: 1}, UIDs: []string{"valid"}},
	} {
		state := &personalAllowlistPorts{}
		err := New(Options{Store: state, PersonDirectory: state, MembershipActivation: state}).AddAllowlist(context.Background(), cmd)
		if !errors.Is(err, metadb.ErrInvalidArgument) || len(state.calls) != 0 {
			t.Fatalf("invalid command %#v: error/calls = %v/%v", cmd, err, state.calls)
		}
	}
	state := &personalAllowlistPorts{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := New(Options{Store: state, PersonDirectory: state, MembershipActivation: state}).AddAllowlist(ctx, MemberCommand{ChannelKey: ChannelKey{ChannelID: "owner", ChannelType: 1}, UIDs: []string{"peer"}})
	if !errors.Is(err, context.Canceled) || len(state.calls) != 0 {
		t.Fatalf("canceled command: error/calls = %v/%v", err, state.calls)
	}
}

func TestPersonalAllowlistPreparesDistinctPairsThenActivatesOnlyOwner(t *testing.T) {
	state := &personalAllowlistPorts{}
	now := time.Unix(100, 123)
	app := New(Options{Store: state, PersonDirectory: state, MembershipActivation: state, Now: func() time.Time { return now }})
	err := app.AddAllowlist(context.Background(), MemberCommand{ChannelKey: ChannelKey{ChannelID: "owner", ChannelType: 1}, UIDs: []string{"peer", "peer", "owner", "other"}})
	if err != nil {
		t.Fatal(err)
	}
	wantPairs := []string{channelid.EncodePersonChannel("owner", "peer"), channelid.EncodePersonChannel("owner", "owner"), channelid.EncodePersonChannel("owner", "other")}
	if !reflect.DeepEqual(state.prepared, wantPairs) || !reflect.DeepEqual(state.activated, wantPairs) {
		t.Fatalf("prepared/activated = %v/%v, want %v", state.prepared, state.activated, wantPairs)
	}
	for index, uid := range state.activationUIDs {
		if uid != "owner" || state.activationTimes[index] != now.UnixNano() {
			t.Fatalf("activation = %s/%d, want owner/%d", uid, state.activationTimes[index], now.UnixNano())
		}
	}
	if !reflect.DeepEqual(state.calls, []string{"acl", "directory", "activation", "directory", "activation", "directory", "activation"}) {
		t.Fatalf("calls = %v", state.calls)
	}
}

func TestNonPersonalAllowlistAndOtherListsDoNotPreparePersonDirectory(t *testing.T) {
	state := &personalAllowlistPorts{}
	app := New(Options{Store: state, PersonDirectory: state, MembershipActivation: state})
	personal := MemberCommand{ChannelKey: ChannelKey{ChannelID: "owner", ChannelType: 1}, UIDs: []string{"peer"}}
	if err := app.AddAllowlist(context.Background(), MemberCommand{ChannelKey: ChannelKey{ChannelID: "group", ChannelType: 2}, UIDs: personal.UIDs}); err != nil {
		t.Fatal(err)
	}
	if err := app.AddDenylist(context.Background(), personal); err != nil {
		t.Fatal(err)
	}
	if err := app.RemoveAllowlist(context.Background(), personal); err != nil {
		t.Fatal(err)
	}
	personal.UIDs = nil
	if err := New(Options{Store: state}).AddAllowlist(context.Background(), personal); err != nil {
		t.Fatal(err)
	}
	if len(state.prepared) != 0 || len(state.activated) != 0 {
		t.Fatalf("unrelated operations prepared/activated = %v/%v", state.prepared, state.activated)
	}
}

type personalAllowlistPorts struct {
	recordingStore
	calls, prepared, activated, activationUIDs []string
	activationTimes                            []int64
	failStage                                  string
	failErr                                    error
}

func (s *personalAllowlistPorts) AddChannelSubscribers(ctx context.Context, id string, typ int64, uids []string, versions ...uint64) error {
	s.calls = append(s.calls, "acl")
	if s.failStage == "acl" {
		return s.failErr
	}
	return s.recordingStore.AddChannelSubscribers(ctx, id, typ, uids, versions...)
}

func (s *personalAllowlistPorts) AdmitPersonChannelDirectory(_ context.Context, id string, typ int64) error {
	s.calls = append(s.calls, "directory")
	s.prepared = append(s.prepared, id)
	if typ != 1 {
		return metadb.ErrInvalidArgument
	}
	if s.failStage == "directory" {
		return s.failErr
	}
	return nil
}

func (s *personalAllowlistPorts) ActivateUserChannelMembership(_ context.Context, uid, id string, typ, activatedAt, updatedAt int64) error {
	s.calls = append(s.calls, "activation")
	s.activated = append(s.activated, id)
	s.activationUIDs = append(s.activationUIDs, uid)
	s.activationTimes = append(s.activationTimes, activatedAt)
	if typ != 1 || updatedAt != activatedAt {
		return metadb.ErrInvalidArgument
	}
	if s.failStage == "activation" {
		return s.failErr
	}
	return nil
}
