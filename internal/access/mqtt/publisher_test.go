package mqtt_test

import (
	"context"
	"errors"
	"testing"
	"time"

	access "github.com/WuKongIM/WuKongIM/internal/access/mqtt"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	gt "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

type publishMessages struct {
	permission func(context.Context, message.PublishPermissionQuery) (message.Reason, error)
	send       func(context.Context, message.SendCommand) (message.SendResult, error)
}

func (m *publishMessages) CheckPublishPermission(ctx context.Context, q message.PublishPermissionQuery) (message.Reason, error) {
	if m.permission != nil {
		return m.permission(ctx, q)
	}
	return message.ReasonSuccess, nil
}
func (m *publishMessages) Send(ctx context.Context, c message.SendCommand) (message.SendResult, error) {
	return m.send(ctx, c)
}

type publishFixture struct {
	publisher  *access.Publisher
	owners     *runtime.Owners
	connection sessioncase.Connection
	gateway    gt.Context
	messages   *publishMessages
	now        time.Time
	writes     []any
	closed     bool
}

func newPublishFixture(t *testing.T) *publishFixture {
	t.Helper()
	f := &publishFixture{now: time.Now()}
	var err error
	f.owners, err = runtime.NewOwners(runtime.OwnerOptions{NodeID: 7, BootID: "publish-test", Capacity: 2, MaxOperations: 1, PendingTimeout: time.Second, MaxLease: time.Minute, CloseRetry: time.Second, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	owner, err := f.owners.Reserve(runtime.Claim{Key: contract.Key{Namespace: "main", ClientID: "SYSTEM"}, UID: "alice", SessionGeneration: 2, OwnerGeneration: 3}, func(context.Context) error { return nil })
	require.NoError(t, err)
	require.NoError(t, f.owners.Activate(owner, 1, f.now.Add(30*time.Second)))
	f.connection = sessioncase.Connection{Owner: owner, UID: "alice", DeviceFlag: 1}
	f.messages = &publishMessages{send: func(context.Context, message.SendCommand) (message.SendResult, error) {
		return message.SendResult{MessageID: 100, MessageSeq: 7}, nil
	}}
	f.publisher, err = access.NewPublisher(access.PublisherOptions{Owners: f.owners, Messages: f.messages, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	f.gateway = gt.Context{RequestContext: context.Background(), Session: session.New(session.Config{ID: 42, WritePacketFn: func(p any, _ session.OutboundMeta) error { f.writes = append(f.writes, p); return nil }}), CloseSessionFn: func(gt.CloseReason, error) { f.closed = true }}
	t.Cleanup(func() {
		uncertain := f.owners.Snapshot().Uncertain
		err := f.owners.Close(context.Background())
		if uncertain > 0 {
			require.ErrorIs(t, err, runtime.ErrOwnerUnknown)
		} else {
			require.NoError(t, err)
		}
	})
	return f
}
func publishPacket() *wire.Publish {
	return &wire.Publish{Topic: "wk/v1/users/Ym9i/messages", Payload: []byte("hello"), QoS: 1, PacketID: 29, Properties: []wire.Property{{ID: wire.UserProperty, Text: "wk.client_msg_no", Value: "stable-1"}, {ID: wire.UserProperty, Text: "app", Value: "value"}}}
}
func (f *publishFixture) run(p *wire.Publish) error {
	return f.publisher.Publish(f.gateway, f.connection, p)
}

func TestPublisherUsesAuthenticatedIdentityAndCommittedACK(t *testing.T) {
	for _, qos := range []byte{0, 1} {
		t.Run(string(rune('0'+qos)), func(t *testing.T) {
			f := newPublishFixture(t)
			packet := publishPacket()
			packet.QoS = qos
			if qos == 0 {
				packet.PacketID = 0
			}
			f.gateway.Session.SetValue(gt.SessionValueUID, "mallory")
			f.gateway.Session.SetValue(gt.SessionValueDeviceID, "SYSTEM")
			permissionCalls, sends := 0, 0
			f.messages.permission = func(ctx context.Context, q message.PublishPermissionQuery) (message.Reason, error) {
				require.Equal(t, message.PublishPermissionQuery{FromUID: "alice", TargetID: "bob", TargetType: 1}, q)
				require.Equal(t, 1, f.owners.Snapshot().Operations)
				_, bounded := ctx.Deadline()
				require.True(t, bounded)
				permissionCalls++
				return message.ReasonSuccess, nil
			}
			f.messages.send = func(ctx context.Context, c message.SendCommand) (message.SendResult, error) {
				require.Equal(t, 1, permissionCalls)
				require.Empty(t, f.writes)
				require.Equal(t, "alice", c.FromUID)
				require.Empty(t, c.DeviceID)
				require.Equal(t, uint8(1), c.DeviceFlag)
				require.Equal(t, uint64(7), c.SenderNodeID)
				require.Equal(t, uint64(42), c.SenderSessionID)
				require.Zero(t, c.MessageID)
				require.Zero(t, c.ClientSeq)
				require.Zero(t, c.ProtocolVersion)
				require.Equal(t, "stable-1", c.ClientMsgNo)
				require.Equal(t, "bob", c.ChannelID)
				require.Equal(t, uint8(1), c.ChannelType)
				require.True(t, c.NormalizePersonChannel)
				require.False(t, c.NoPersist)
				require.False(t, c.SyncOnce)
				require.False(t, c.SkipPluginHooks)
				require.Equal(t, message.SendOriginClient, c.Origin)
				meta, err := publication.Decode(c.PublicationMetadata)
				require.NoError(t, err)
				require.Equal(t, qos, meta.QoS)
				require.Equal(t, "SYSTEM", meta.PublisherClientID)
				require.Equal(t, f.now.UnixMilli(), meta.AcceptedAtMS)
				packet.Payload[0] = 'X'
				require.Equal(t, "hello", string(c.Payload))
				sends++
				return message.SendResult{MessageID: 100, MessageSeq: 7}, nil
			}
			require.NoError(t, f.run(packet))
			require.Equal(t, 1, sends)
			require.False(t, f.closed)
			require.Zero(t, f.owners.Snapshot().Operations)
			if qos == 0 {
				require.Empty(t, f.writes)
			} else {
				require.Equal(t, []any{&wire.Puback{PacketID: 29}}, f.writes)
			}
		})
	}
}

func TestPublisherRefusesUnownedOrMalformedInput(t *testing.T) {
	for _, mode := range []string{"uid", "flag", "owner", "expired", "cancelled", "nil", "pid", "qos0dup", "retained", "topic", "property"} {
		t.Run(mode, func(t *testing.T) {
			f := newPublishFixture(t)
			p := publishPacket()
			calls := 0
			f.messages.permission = func(context.Context, message.PublishPermissionQuery) (message.Reason, error) {
				calls++
				return message.ReasonSuccess, nil
			}
			f.messages.send = func(context.Context, message.SendCommand) (message.SendResult, error) {
				calls++
				return message.SendResult{}, nil
			}
			switch mode {
			case "uid":
				f.connection.UID = "mallory"
			case "flag":
				f.connection.DeviceFlag = 99
			case "owner":
				f.connection.Owner.OwnerGeneration++
			case "expired":
				f.now = f.now.Add(time.Minute)
			case "cancelled":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				f.gateway.RequestContext = ctx
			case "nil":
				p = nil
			case "pid":
				p.PacketID = 0
			case "qos0dup":
				p.QoS, p.PacketID, p.Dup = 0, 0, true
			case "retained":
				p.Retain = true
			case "topic":
				p.Topic = "#"
			case "property":
				p.Properties = append(p.Properties, wire.Property{ID: wire.UserProperty, Text: "wk.from_uid", Value: "mallory"})
			}
			require.Error(t, f.run(p))
			require.Zero(t, calls)
			require.True(t, f.closed)
			for _, w := range f.writes {
				require.IsType(t, &wire.Disconnect{}, w)
			}
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}

func TestPublisherDefiniteRejectionAndUncertainSendRemainDistinct(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason message.Reason
		want   byte
	}{
		{"auth", message.ReasonAuthFail, 0x87}, {"membership", message.ReasonSubscriberNotExist, 0x87},
		{"ban", message.ReasonSendBan, 0x87}, {"disband", message.ReasonDisband, 0x87},
		{"topic", message.ReasonChannelNotExist, 0x90}, {"invalid", message.ReasonInvalidRequest, 0x90},
		{"business", message.Reason(200), 0x83},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, stage := range []string{"permission", "send"} {
				f := newPublishFixture(t)
				if stage == "permission" {
					f.messages.permission = func(context.Context, message.PublishPermissionQuery) (message.Reason, error) { return tc.reason, nil }
					f.messages.send = func(context.Context, message.SendCommand) (message.SendResult, error) {
						t.Fatal("denied permission submitted")
						return message.SendResult{}, nil
					}
				} else {
					f.messages.send = func(context.Context, message.SendCommand) (message.SendResult, error) {
						return message.SendResult{Reason: tc.reason}, nil
					}
				}
				require.NoError(t, f.run(publishPacket()))
				require.Equal(t, []any{&wire.Puback{PacketID: 29, Reason: tc.want}}, f.writes)
				require.False(t, f.closed)
			}
		})
	}
	for _, mode := range []string{"error", "success_with_error", "zero_id", "zero_seq", "route", "system", "unknown", "permission_error", "write_error", "panic", "write_panic", "contradictory_rejection"} {
		t.Run(mode, func(t *testing.T) {
			f := newPublishFixture(t)
			f.messages.send = func(context.Context, message.SendCommand) (message.SendResult, error) {
				switch mode {
				case "error":
					return message.SendResult{}, errors.New("secret payload")
				case "success_with_error":
					return message.SendResult{MessageID: 100, MessageSeq: 1}, errors.New("secret")
				case "zero_id":
					return message.SendResult{MessageSeq: 1}, nil
				case "zero_seq":
					return message.SendResult{MessageID: 100}, nil
				case "route":
					return message.SendResult{Reason: message.ReasonNodeNotMatch}, nil
				case "system":
					return message.SendResult{Reason: message.ReasonSystemError}, nil
				case "unknown":
					return message.SendResult{Reason: 100}, nil
				case "panic":
					panic("secret payload")
				case "contradictory_rejection":
					return message.SendResult{MessageID: 100, MessageSeq: 1, Reason: message.ReasonBan}, nil
				default:
					return message.SendResult{MessageID: 100, MessageSeq: 1}, nil
				}
			}
			if mode == "permission_error" {
				f.messages.permission = func(context.Context, message.PublishPermissionQuery) (message.Reason, error) {
					return message.ReasonBan, errors.New("secret credential")
				}
			}
			if mode == "write_error" || mode == "write_panic" {
				f.gateway.Session = session.New(session.Config{ID: 42, WritePacketFn: func(any, session.OutboundMeta) error {
					if mode == "write_panic" {
						panic("secret")
					}
					return errors.New("secret transport")
				}})
			}
			err := f.run(publishPacket())
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret")
			require.True(t, f.closed)
			require.Zero(t, f.owners.Snapshot().Operations)
			for _, w := range f.writes {
				require.IsType(t, &wire.Disconnect{}, w)
			}
			_, err = f.owners.Begin(context.Background(), f.connection.Owner)
			require.Error(t, err)
		})
	}
}

func TestPublisherRechecksFenceAndDeadlineAfterDependencies(t *testing.T) {
	for _, stage := range []string{"permission", "send"} {
		for _, mode := range []string{"fence", "expiry", "cancel"} {
			t.Run(stage+mode, func(t *testing.T) {
				f := newPublishFixture(t)
				parent, cancel := context.WithCancel(context.Background())
				defer cancel()
				f.gateway.RequestContext = parent
				interrupt := func() {
					switch mode {
					case "fence":
						require.NoError(t, f.owners.Fence(f.connection.Owner))
					case "expiry":
						f.now = f.now.Add(time.Minute)
					case "cancel":
						cancel()
					}
				}
				sends := 0
				f.messages.permission = func(context.Context, message.PublishPermissionQuery) (message.Reason, error) {
					if stage == "permission" {
						interrupt()
					}
					return message.ReasonSuccess, nil
				}
				f.messages.send = func(context.Context, message.SendCommand) (message.SendResult, error) {
					sends++
					interrupt()
					return message.SendResult{MessageID: 100, MessageSeq: 1}, nil
				}
				require.Error(t, f.run(publishPacket()))
				require.True(t, f.closed)
				require.Empty(t, f.writes)
				if stage == "permission" {
					require.Zero(t, sends)
				} else {
					require.Equal(t, 1, sends)
				}
				require.Zero(t, f.owners.Snapshot().Operations)
			})
		}
	}
}

func TestPublisherOperationCoversReplyAndJoinsTakeover(t *testing.T) {
	f := newPublishFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	f.gateway.Session = session.New(session.Config{ID: 42, WritePacketFn: func(any, session.OutboundMeta) error { close(entered); <-release; return nil }})
	completed := make(chan error, 1)
	go func() { completed <- f.run(publishPacket()) }()
	<-entered
	require.Equal(t, 1, f.owners.Snapshot().Operations)
	require.NoError(t, f.owners.Fence(f.connection.Owner))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, f.owners.Quiesce(canceled, f.connection.Owner))
	require.Equal(t, 1, f.owners.Snapshot().Operations)
	close(release)
	require.NoError(t, <-completed)
	require.NoError(t, f.owners.Quiesce(context.Background(), f.connection.Owner))
	require.Zero(t, f.owners.Snapshot().Held)
}

func TestPublisherQoSZeroDenialClosesWithoutPUBACK(t *testing.T) {
	f := newPublishFixture(t)
	p := publishPacket()
	p.QoS, p.PacketID = 0, 0
	f.messages.permission = func(context.Context, message.PublishPermissionQuery) (message.Reason, error) {
		return message.ReasonBan, nil
	}
	require.Error(t, f.run(p))
	require.True(t, f.closed)
	require.Equal(t, []any{&wire.Disconnect{Reason: 0x87}}, f.writes)
}

func TestPublisherOptionsRequireDependenciesAndBoundTimeout(t *testing.T) {
	f := newPublishFixture(t)
	for _, o := range []access.PublisherOptions{{}, {Owners: f.owners}, {Messages: f.messages}, {Owners: f.owners, Messages: f.messages, Timeout: -1}, {Owners: f.owners, Messages: f.messages, Timeout: time.Minute + 1}} {
		_, err := access.NewPublisher(o)
		require.Error(t, err)
	}
}

func TestPublisherPropagatesOwnerCancellationIntoDependency(t *testing.T) {
	f := newPublishFixture(t)
	f.messages.send = func(ctx context.Context, _ message.SendCommand) (message.SendResult, error) {
		require.NoError(t, f.owners.Fence(f.connection.Owner))
		require.ErrorIs(t, ctx.Err(), context.Canceled, "takeover must cancel the actual dependency context")
		return message.SendResult{}, ctx.Err()
	}
	require.Error(t, f.run(publishPacket()))
	require.Empty(t, f.writes)
}

func TestPublisherUncertainAppendBlocksTakeoverProof(t *testing.T) {
	f := newPublishFixture(t)
	f.messages.send = func(context.Context, message.SendCommand) (message.SendResult, error) {
		return message.SendResult{}, context.DeadlineExceeded
	}
	require.Error(t, f.run(publishPacket()))
	require.ErrorIs(t, f.owners.Quiesce(context.Background(), f.connection.Owner), runtime.ErrOwnerUnknown)
	require.Equal(t, 1, f.owners.Snapshot().Uncertain)
}
