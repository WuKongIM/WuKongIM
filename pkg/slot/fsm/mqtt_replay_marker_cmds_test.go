package fsm

import (
	"bytes"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestMQTTReplayMarkerClearCommandRejectsMalformed(t *testing.T) {
	owner := mqttSourceBindingCommandFixture().Key.Owner
	data, err := EncodeMQTTReplayMarkerClearCommand(owner)
	require.NoError(t, err)
	require.Equal(t, []byte{1, 78}, data[:2])
	in, err := DecodeCommandInspection(data)
	require.NoError(t, err)
	require.Equal(t, "mqtt_replay_marker_clear", in.Type)

	_, err = EncodeMQTTReplayMarkerClearCommand(metadb.MQTTBindingOwner{})
	require.Error(t, err)
	uid := owner
	uid.Kind = metadb.MQTTBindingUID
	_, err = EncodeMQTTReplayMarkerClearCommand(uid)
	require.Error(t, err)
	for _, raw := range [][]byte{{1, 78}, append(bytes.Clone(data), []byte(`{}`)...), append([]byte{1, 78}, []byte(`{"version":2}`)...)} {
		_, err := DecodeCommandInspection(raw)
		require.Error(t, err)
	}
}
