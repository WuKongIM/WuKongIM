package proxy

import (
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// TestProxyRPCBinaryFramesRejectEveryTruncatedPrefix protects the fail-closed
// boundary shared by the proxy RPC handlers. A partially delivered frame must
// never be mistaken for an older or empty request/response.
func TestProxyRPCBinaryFramesRejectEveryTruncatedPrefix(t *testing.T) {
	afterRuntimeMeta := metadb.ChannelRuntimeMetaCursor{ChannelID: "before", ChannelType: 2}
	runtimeMeta := metadb.ChannelRuntimeMeta{
		ChannelID:            "channel-a",
		ChannelType:          2,
		ChannelEpoch:         11,
		LeaderEpoch:          7,
		Replicas:             []uint64{1, 2, 3},
		ISR:                  []uint64{1, 2},
		Leader:               1,
		MinISR:               2,
		Status:               1,
		Features:             3,
		LeaseUntilMS:         1234,
		RetentionThroughSeq:  99,
		RetentionUpdatedAtMS: 5678,
		WriteFenceToken:      "fence-token",
		WriteFenceVersion:    5,
		WriteFenceReason:     1,
		WriteFenceUntilMS:    9012,
		RouteGeneration:      6,
	}
	afterPluginBinding := pluginBindingRPCCursor{PluginNo: "plugin-a", UID: "user-a"}
	ordinaryMembership := metadb.UserChannelMembership{
		UID: "user-a", ChannelID: "channel-a", ChannelType: 2,
		JoinSeq: 10, ReadSeq: 9, DeletedToSeq: 3, ActivatedAt: 100,
		Tombstone: true, TombstoneAt: 101, SourceVersion: 4, UpdatedAt: 102,
	}

	tests := []struct {
		name   string
		encode func(t *testing.T) []byte
		decode func([]byte) error
	}{
		{
			name: "identity request",
			encode: func(t *testing.T) []byte {
				body, err := encodeIdentityRPCRequestBinary(identityRPCRequest{
					Op: identityRPCScanUsersPage, SlotID: 7, UID: "user-a", DeviceFlag: 3,
					After: metadb.UserCursor{UID: "before"}, Limit: 64,
				})
				mustEncodeRPCFrame(t, err)
				return body
			},
			decode: func(body []byte) error { _, err := decodeIdentityRPCRequest(body); return err },
		},
		{
			name: "identity response",
			encode: func(t *testing.T) []byte {
				body, err := encodeIdentityRPCResponseBinary(identityRPCResponse{
					Status: rpcStatusOK, LeaderID: 9,
					User:   &metadb.User{UID: "user-a", Token: "token-a", DeviceFlag: 3, DeviceLevel: 1},
					Device: &metadb.Device{UID: "user-a", DeviceFlag: 3, Token: "device-token", DeviceLevel: 2},
					Users: []metadb.User{
						{UID: "user-b", Token: "token-b", DeviceFlag: 1, DeviceLevel: 2},
						{UID: "user-c", Token: "token-c", DeviceFlag: 2, DeviceLevel: 1},
					},
					Cursor: metadb.UserCursor{UID: "user-c"}, Done: true,
				})
				mustEncodeRPCFrame(t, err)
				return body
			},
			decode: func(body []byte) error { _, err := decodeIdentityRPCResponseBinary(body); return err },
		},
		{
			name: "subscriber request",
			encode: func(t *testing.T) []byte {
				body, err := encodeSubscriberRPCRequestBinary(subscriberRPCRequest{
					SlotID: 7, HashSlot: 23, ChannelID: "channel-a", ChannelType: 2,
					Snapshot: true, AfterUID: "user-a", Limit: 64, ContainsUID: "user-b", HasAny: true,
				})
				mustEncodeRPCFrame(t, err)
				return body
			},
			decode: func(body []byte) error { _, err := decodeSubscriberRPCRequest(body); return err },
		},
		{
			name: "subscriber response",
			encode: func(t *testing.T) []byte {
				body, err := encodeSubscriberRPCResponseBinary(subscriberRPCResponse{
					Status: rpcStatusOK, LeaderID: 9, UIDs: []string{"user-a", "user-b"},
					NextCursor: "user-b", Done: true, Contains: true, HasAny: true,
				})
				mustEncodeRPCFrame(t, err)
				return body
			},
			decode: func(body []byte) error { _, err := decodeSubscriberRPCResponseBinary(body); return err },
		},
		{
			name: "permission batch request",
			encode: func(t *testing.T) []byte {
				body, err := encodePermissionBatchRPCRequest(permissionBatchRPCRequest{
					SlotID: 7,
					Reads: []PermissionMetadataRead{
						{Kind: PermissionMetadataReadChannel, ChannelID: "channel-a", ChannelType: 2},
						{Kind: PermissionMetadataReadSubscriberContains, ChannelID: "channel-a", ChannelType: 2, UID: "user-a"},
					},
				})
				mustEncodeRPCFrame(t, err)
				return body
			},
			decode: func(body []byte) error { _, err := decodePermissionBatchRPCRequest(body); return err },
		},
		{
			name: "permission batch response",
			encode: func(t *testing.T) []byte {
				body, err := encodePermissionBatchRPCResponse(permissionBatchRPCResponse{
					Status: rpcStatusOK, LeaderID: 9,
					Results: []PermissionMetadataReadResult{
						{Found: true, Channel: metadb.Channel{ChannelID: "channel-a", ChannelType: 2, Ban: 1}},
						{Value: true},
					},
				})
				mustEncodeRPCFrame(t, err)
				return body
			},
			decode: func(body []byte) error { _, err := decodePermissionBatchRPCResponse(body); return err },
		},
		{
			name: "membership request",
			encode: func(t *testing.T) []byte {
				body, err := encodeMembershipRPCRequest(membershipRPCRequest{
					Op: membershipRPCListOrdinary, SlotID: 7, UID: "user-a",
					ChannelID: "channel-a", ChannelType: 2,
					OrdinaryCursor: metadb.UserChannelMembershipCursor{ActivatedAt: 10, ChannelID: "before", ChannelType: 2},
					CMDCursor:      metadb.UserCMDChannelMembershipCursor{CommandChannelID: "cmd-before", ChannelType: 2},
					Limit:          64,
				})
				mustEncodeRPCFrame(t, err)
				return body
			},
			decode: func(body []byte) error { _, err := decodeMembershipRPCRequest(body); return err },
		},
		{
			name: "membership response",
			encode: func(t *testing.T) []byte {
				body, err := encodeMembershipRPCResponse(membershipRPCResponse{
					Status: rpcStatusOK, LeaderID: 9, Membership: &ordinaryMembership,
					Memberships:    []metadb.UserChannelMembership{ordinaryMembership},
					OrdinaryCursor: metadb.UserChannelMembershipCursor{ActivatedAt: 100, ChannelID: "channel-a", ChannelType: 2},
					CMDMemberships: []metadb.UserCMDChannelMembership{{
						UID: "user-a", CommandChannelID: "cmd-channel", ChannelType: 2,
						StartSeq: 1, AckSeq: 2, Tombstone: true, TombstoneAt: 10, UpdatedAt: 11,
					}},
					CMDCursor: metadb.UserCMDChannelMembershipCursor{CommandChannelID: "cmd-channel", ChannelType: 2}, Done: true,
				})
				mustEncodeRPCFrame(t, err)
				return body
			},
			decode: func(body []byte) error { _, err := decodeMembershipRPCResponse(body); return err },
		},
		{
			name: "channel request",
			encode: func(t *testing.T) []byte {
				body, err := encodeChannelRPCRequestBinary(channelRPCRequest{
					Op: channelRPCScanChannelsPage, SlotID: 7, HashSlot: 23,
					ChannelID: "channel-a", ChannelType: 2,
					After: metadb.ChannelCursor{ChannelID: "before", ChannelType: 1}, Limit: 64,
				})
				mustEncodeRPCFrame(t, err)
				return body
			},
			decode: func(body []byte) error { _, err := decodeChannelRPCRequest(body); return err },
		},
		{
			name: "channel response",
			encode: func(t *testing.T) []byte {
				return encodeChannelRPCResponseBinary(channelRPCResponse{
					Status: rpcStatusOK, LeaderID: 9,
					Channel:  &metadb.Channel{ChannelID: "channel-a", ChannelType: 2, Ban: 1},
					Channels: []metadb.Channel{{ChannelID: "channel-b", ChannelType: 1, AllowStranger: 1}},
					Cursor:   metadb.ChannelCursor{ChannelID: "channel-b", ChannelType: 1}, Done: true,
				})
			},
			decode: func(body []byte) error { _, err := decodeChannelRPCResponseBinary(body); return err },
		},
		{
			name: "runtime meta request",
			encode: func(t *testing.T) []byte {
				body, err := encodeRuntimeMetaRPCRequestBinary(runtimeMetaRPCRequest{
					Op: runtimeMetaRPCScanPage, SlotID: 7, ChannelID: "channel-a", ChannelType: 2,
					Keys:  []metadb.ChannelKey{{ChannelID: "channel-a", ChannelType: 2}, {ChannelID: "channel-b", ChannelType: 1}},
					After: &afterRuntimeMeta, Limit: 64, CodecVersion: 3,
				})
				mustEncodeRPCFrame(t, err)
				return body
			},
			decode: func(body []byte) error { _, err := decodeRuntimeMetaRPCRequest(body); return err },
		},
		{
			name: "runtime meta response",
			encode: func(t *testing.T) []byte {
				body, err := encodeRuntimeMetaRPCResponseBinary(runtimeMetaRPCResponse{
					Status: rpcStatusOK, LeaderID: 9, Meta: &runtimeMeta,
					Metas:  []metadb.ChannelRuntimeMeta{runtimeMeta},
					Cursor: metadb.ChannelRuntimeMetaCursor{ChannelID: "channel-a", ChannelType: 2}, Done: true,
				})
				mustEncodeRPCFrame(t, err)
				return body
			},
			decode: func(body []byte) error { _, err := decodeRuntimeMetaRPCResponseBinary(body); return err },
		},
		{
			name: "plugin binding request",
			encode: func(t *testing.T) []byte {
				body, err := encodePluginBindingRPCRequestBinary(pluginBindingRPCRequest{
					Op: pluginBindingRPCScanByPluginNo, SlotID: 7, HashSlot: 23,
					UID: "user-a", PluginNo: "plugin-a", After: &afterPluginBinding, Limit: 64,
				})
				mustEncodeRPCFrame(t, err)
				return body
			},
			decode: func(body []byte) error { _, err := decodePluginBindingRPCRequest(body); return err },
		},
		{
			name: "plugin binding response",
			encode: func(t *testing.T) []byte {
				body, err := encodePluginBindingRPCResponse(pluginBindingRPCResponse{
					Status: rpcStatusOK, LeaderID: 9,
					Bindings: []metadb.PluginUserBinding{{UID: "user-a", PluginNo: "plugin-a", CreatedAtMS: 100, UpdatedAtMS: 101}},
					Cursor:   afterPluginBinding, Done: true, Exists: true,
				})
				mustEncodeRPCFrame(t, err)
				return body
			},
			decode: func(body []byte) error { _, err := decodePluginBindingRPCResponse(body); return err },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := test.encode(t)
			if err := test.decode(body); err != nil {
				t.Fatalf("valid frame rejected: %v", err)
			}
			for cut := 0; cut < len(body); cut++ {
				if err := test.decode(body[:cut]); err == nil {
					t.Fatalf("truncated frame accepted at byte %d of %d", cut, len(body))
				}
			}

			withTrailingByte := append(append([]byte(nil), body...), 0)
			if err := test.decode(withTrailingByte); err == nil {
				t.Fatal("frame with trailing data accepted")
			}
		})
	}
}

func mustEncodeRPCFrame(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("encode valid RPC frame: %v", err)
	}
}
