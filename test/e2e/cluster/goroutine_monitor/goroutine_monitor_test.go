//go:build e2e

package goroutine_monitor

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestThreeNodeManagerReportsGoroutineOwnershipByNodeAndModule(t *testing.T) {
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(suite.WithManagerHTTP())

	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	queryCtx, cancelQuery := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelQuery()
	var response goroutineMonitorResponse
	_, err := suite.GetJSON(queryCtx, "http://"+cluster.MustNode(1).ManagerAddr()+"/manager/realtime-monitor?category=goroutines&window=5m", &response)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, "ready", response.Status)
	require.True(t, response.Sources.Goroutines.Enabled)
	require.Len(t, response.Goroutines.Nodes, 3)

	for index, node := range response.Goroutines.Nodes {
		require.Equal(t, uint64(index+1), node.NodeID)
		require.True(t, node.Supported, "node %d error=%s", node.NodeID, node.Error)
		require.NotNil(t, node.Snapshot, "node %d snapshot", node.NodeID)
		require.NotEmpty(t, node.Snapshot.BootID, "node %d boot id", node.NodeID)
		require.Positive(t, node.Snapshot.ProcessTotal, "node %d process total", node.NodeID)
		require.Positive(t, node.Snapshot.ManagedTotal, "node %d managed total", node.NodeID)
		require.True(t, hasActiveModule(node.Snapshot.Modules, "cluster"), "node %d missing active cluster ownership", node.NodeID)
		requireNoCriticalModules(t, node)
		require.Equal(t, "normal", taskHealth(node.Snapshot.Modules, "cluster/node_control_watch"), "node %d node control watch health", node.NodeID)
		require.Equal(t, "normal", taskHealth(node.Snapshot.Modules, "cluster/runtime_control_watch"), "node %d runtime control watch health", node.NodeID)
		require.Equal(t, "normal", taskHealth(node.Snapshot.Modules, "transport/client_observer"), "node %d client observer health", node.NodeID)
		require.Equal(t, "normal", taskHealth(node.Snapshot.Modules, "transport/server_observer"), "node %d server observer health", node.NodeID)
	}

	selectedCtx, cancelSelected := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelSelected()
	var selected goroutineMonitorResponse
	_, err = suite.GetJSON(selectedCtx, fmt.Sprintf(
		"http://%s/manager/realtime-monitor?category=goroutines&window=5m&node_id=2",
		cluster.MustNode(1).ManagerAddr(),
	), &selected)
	require.NoError(t, err)
	require.Len(t, selected.Goroutines.Nodes, 1)
	require.Equal(t, uint64(2), selected.Goroutines.Nodes[0].NodeID)
	require.True(t, selected.Goroutines.Nodes[0].Supported)
}

func hasActiveModule(modules []goroutineModuleDTO, name string) bool {
	for _, module := range modules {
		if module.Module == name && module.Active > 0 {
			return true
		}
	}
	return false
}

func requireNoCriticalModules(t *testing.T, node goroutineNodeDTO) {
	t.Helper()
	for _, module := range node.Snapshot.Modules {
		require.NotEqual(t, "critical", module.Health, "node %d module %s health", node.NodeID, module.Module)
	}
}

func taskHealth(modules []goroutineModuleDTO, id string) string {
	for _, module := range modules {
		for _, task := range module.Tasks {
			if task.Task == id && task.Active == 1 {
				return task.Health
			}
		}
	}
	return ""
}

type goroutineMonitorResponse struct {
	Status  string `json:"status"`
	Sources struct {
		Goroutines struct {
			Enabled bool `json:"enabled"`
		} `json:"goroutines"`
	} `json:"sources"`
	Goroutines struct {
		Nodes []goroutineNodeDTO `json:"nodes"`
	} `json:"goroutines"`
}

type goroutineNodeDTO struct {
	NodeID    uint64                `json:"node_id"`
	Supported bool                  `json:"supported"`
	Error     string                `json:"error"`
	Snapshot  *goroutineSnapshotDTO `json:"snapshot"`
}

type goroutineSnapshotDTO struct {
	BootID       string               `json:"boot_id"`
	ProcessTotal int64                `json:"process_total"`
	ManagedTotal int64                `json:"managed_total"`
	Modules      []goroutineModuleDTO `json:"modules"`
}

type goroutineModuleDTO struct {
	Module string             `json:"module"`
	Active int64              `json:"active"`
	Health string             `json:"health"`
	Tasks  []goroutineTaskDTO `json:"tasks"`
}

type goroutineTaskDTO struct {
	Task   string `json:"task"`
	Active int64  `json:"active"`
	Health string `json:"health"`
}
