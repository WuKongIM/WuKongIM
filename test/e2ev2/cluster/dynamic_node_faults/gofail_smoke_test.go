//go:build e2e

package dynamic_node_faults

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

const gofailDynamicNodeEnv = "WK_E2EV2_GOFAIL_DYNAMIC_NODE"

func requireGofailDynamicNodeEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv(gofailDynamicNodeEnv) != "1" {
		t.Skipf("set %s=1 to run gofail dynamic-node e2ev2 tests", gofailDynamicNodeEnv)
	}
	if strings.TrimSpace(os.Getenv("WK_E2EV2_BINARY")) == "" {
		t.Skip("set WK_E2EV2_BINARY to a gofail-enabled cmd/wukongimv2 binary")
	}
}

func TestGofailDynamicNodeBinaryExposesFailpoints(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	node1Fail := suite.ReserveGofailEndpoint(t)
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken("e2ev2-gofail-smoke-token"),
		suite.WithNodeEnv(1, node1Fail.Env()),
	)

	readyCtx, readyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer readyCancel()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	listCtx, listCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer listCancel()
	body, err := node1Fail.WaitListed(
		listCtx,
		"wkSlotReplicaMovePromoteLearnerDelay",
		"wkSlotReplicaMoveTransferLeaderDelay",
		"wkSlotReplicaMoveRemoveVoterDelay",
		"wkClusterNetCallShardFault",
		"wkClusterNetCallShardOwnedFault",
		"wkClusterNetSendFault",
		"wkClusterNetSendOwnedFault",
		"wkReportNodeHealthFault",
		"wkControllerV2StateEventDrop",
	)
	require.NoError(t, err, "gofail body:\n%s\n%s", body, cluster.DumpDiagnostics())
	require.NotEmpty(t, strings.TrimSpace(body), cluster.DumpDiagnostics())
}
