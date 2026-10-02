package meta

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMQTTCursorCancelInitRequiresClosedAdmissionAndNeverResetsProgress(t *testing.T) {
	for _, stage := range []string{"preparing", "active", "removing", "removed", "replacement"} {
		t.Run(stage, func(t *testing.T) {
			s := openTestMetaStore(t)
			defer s.close(t)
			session := prepareMQTTCursorSession(t, s.db)
			sub := mqttSubscriptionFixture()
			move := func(target MQTTSubscriptionStage) {
				sub.Stage = target
				if target == MQTTSubscriptionActive || target == MQTTSubscriptionRemoved {
					sub.RecoveryAtMS = 0
				} else {
					sub.RecoveryAtMS = 1000
				}
				m := mqttSubscriptionMutation(session, sub)
				require.Equal(t, MQTTSessionCASApplied, writeMQTTSubscription(t, s.db, m).Status)
				session.Revision++
				sub = m.Subscription
			}
			if stage == "active" {
				move(MQTTSubscriptionActive)
			} else if stage != "preparing" {
				move(MQTTSubscriptionRemoving)
				if stage != "removing" {
					move(MQTTSubscriptionRemoved)
				}
				if stage == "replacement" {
					sub.Generation, sub.OperationID, sub.AuthorizationVersion = session.Revision+1, "replacement", 77
					move(MQTTSubscriptionPreparing)
				}
			}
			m := mqttCursorMutation(session)
			if stage != "preparing" && stage != "active" {
				require.Equal(t, MQTTSessionCASConflict, writeMQTTCursor(t, s.db, m).Status)
			}
			m.Op = MQTTCursorCancelInit
			r := writeMQTTCursor(t, s.db, m)
			if stage == "preparing" || stage == "active" {
				require.Equal(t, MQTTSessionCASConflict, r.Status)
				return
			}
			require.Equal(t, MQTTSessionCASApplied, r.Status)
			require.Equal(t, MQTTSessionCASUnchanged, writeMQTTCursor(t, s.db, m).Status)
			c, found, err := s.db.HashSlot(7).GetMQTTDeliveryCursor(context.Background(), m.Key)
			require.NoError(t, err)
			require.True(t, found)
			require.EqualValues(t, 100, c.StartAfter)
			require.EqualValues(t, 100, c.CompletedThrough)
			require.Zero(t, c.PendingMessages)
			changed := m
			changed.ExpectedRevision = r.CurrentRevision
			changed.Through++
			require.Equal(t, MQTTSessionCASConflict, writeMQTTCursor(t, s.db, changed).Status)
			changed.Op = MQTTCursorInit
			require.Equal(t, MQTTSessionCASConflict, writeMQTTCursor(t, s.db, changed).Status)
			changed.Op, changed.AddedMessages, changed.AddedBytes = MQTTCursorAccount, 1, 10
			require.Equal(t, MQTTSessionCASConflict, writeMQTTCursor(t, s.db, changed).Status)
			changed.Op = MQTTCursorCancelInit
			require.Error(t, ValidateMQTTDeliveryCursorMutation(changed))
			stored, _, err := s.db.HashSlot(7).GetMQTTDeliveryCursor(context.Background(), m.Key)
			require.NoError(t, err)
			require.Equal(t, c, stored)
		})
	}
}
