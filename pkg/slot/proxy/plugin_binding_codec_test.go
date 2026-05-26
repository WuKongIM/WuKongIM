package proxy

import (
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestPluginBindingRPCBinaryCodecRoundTripsOperations(t *testing.T) {
	after := &pluginBindingRPCCursor{PluginNo: "bot-a", UID: "u0"}
	reqs := []pluginBindingRPCRequest{
		{Op: pluginBindingRPCBind, SlotID: 2, HashSlot: 7, UID: "u1", PluginNo: "bot-a"},
		{Op: pluginBindingRPCUnbind, SlotID: 2, HashSlot: 7, UID: "u1", PluginNo: "bot-a"},
		{Op: pluginBindingRPCListByUID, SlotID: 2, HashSlot: 7, UID: "u1"},
		{Op: pluginBindingRPCScanByPluginNo, SlotID: 2, HashSlot: 7, PluginNo: "bot-a", After: after, Limit: 64},
		{Op: pluginBindingRPCExistsByUID, SlotID: 2, HashSlot: 7, UID: "u1"},
		{Op: pluginBindingRPCGetInHashSlot, SlotID: 2, HashSlot: 7, UID: "u1", PluginNo: "bot-a"},
	}

	for _, req := range reqs {
		t.Run(req.Op, func(t *testing.T) {
			body, err := encodePluginBindingRPCRequestBinary(req)
			require.NoError(t, err)
			require.True(t, isPluginBindingRPCRequestBinary(body))

			got, err := decodePluginBindingRPCRequest(body)
			require.NoError(t, err)
			require.Equal(t, req, got)
		})
	}

	resp := pluginBindingRPCResponse{
		Status:   rpcStatusOK,
		LeaderID: 2,
		Bindings: []metadb.PluginUserBinding{{
			UID: "u1", PluginNo: "bot-a", CreatedAtMS: 100, UpdatedAtMS: 101,
		}},
		Cursor: pluginBindingRPCCursor{PluginNo: "bot-a", UID: "u1"},
		Done:   true,
		Exists: true,
	}
	body, err := encodePluginBindingRPCResponse(resp)
	require.NoError(t, err)
	require.True(t, isPluginBindingRPCResponseBinary(body))

	gotResp, err := decodePluginBindingRPCResponse(body)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestPluginBindingPageCursorRoundTripsAndRejectsInvalidInput(t *testing.T) {
	cursor := pluginBindingPageCursor{
		SlotID:   multiraft.SlotID(2),
		HashSlot: 7,
		Binding:  metadb.PluginUserBindingCursor{PluginNo: "bot-a", UID: "u1"},
	}
	encoded, err := encodePluginBindingPageCursor(cursor)
	require.NoError(t, err)
	require.NotEmpty(t, encoded)

	decoded, err := decodePluginBindingPageCursor(encoded)
	require.NoError(t, err)
	require.Equal(t, cursor, decoded)

	_, err = decodePluginBindingPageCursor("not-base64")
	require.Error(t, err)
	_, err = decodePluginBindingPageCursor(encoded[:len(encoded)-1])
	require.Error(t, err)
	_, err = decodePluginBindingPageCursor(string(make([]byte, pluginBindingPageCursorMaxEncodedLen+1)))
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}
