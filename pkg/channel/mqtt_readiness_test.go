package channel

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMQTTReplayReadinessAssociation(t *testing.T) {
	for _, p := range []MQTTReplayReadiness{{CommittedThrough: 0, Covered: true}, {CommittedThrough: 10, Covered: true}, {CommittedThrough: 10, AnchorPosition: 9, RequiredThrough: 8, Covered: false}, {CommittedThrough: 10, AnchorPosition: 9, RequiredThrough: 8, Covered: true}} {
		require.True(t, p.ValidFor(p.CommittedThrough))
		require.False(t, p.ValidFor(p.CommittedThrough+1))
	}
	for _, p := range []MQTTReplayReadiness{{CommittedThrough: 10}, {CommittedThrough: 10, RequiredThrough: 8, Covered: true}, {CommittedThrough: 10, AnchorPosition: 9, Covered: true}, {CommittedThrough: 10, AnchorPosition: 9, RequiredThrough: 9, Covered: true}, {CommittedThrough: 10, AnchorPosition: 11, RequiredThrough: 8, Covered: true}} {
		require.False(t, p.ValidFor(10))
	}
}
