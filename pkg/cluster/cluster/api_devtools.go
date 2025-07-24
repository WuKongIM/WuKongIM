package cluster

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
)

// 修复node的日志
func (s *Server) checkLogConflict(c *wkhttp.Context) {
	// 	// 日志类型
	// 	logType := LogType(wkutil.ParseInt(c.Query("log_type")))
	// 	if logType == LogTypeUnknown {
	// 		logType = LogTypeConfig
	// 	}

	// 	// 开始日志下标
	// 	startLogIndex := wkutil.ParseUint64(c.Query("start_log_index"))

	// 	// 获取当前所有节点
	// 	nodes := s.cfgServer.Nodes()

	// 	if len(nodes) == 1 {
	// 		c.ResponseError(fmt.Errorf("单节点不存在日志冲突问题"))
	// 		return
	// 	}

	// 	// 如果节点有离线的则忽略检查（必须所有节点在线才启动检查）
	// 	for _, node := range nodes {
	// 		if !node.Online {
	// 			c.ResponseError(fmt.Errorf("node[%d] is offline", node.Id))
	// 			return
	// 		}
	// 	}

	// 	// 获取每个节点的日志，并按节点分组
	// 	nodeLogs := make(map[uint64][]rafttypes.Log)
	// 	var err error
	// 	var limit uint32 = 1000
	// 	var maxLoop = 10

	// 	for maxLoop > 0 {
	// 		for _, node := range nodes {
	// 			logs, err := s.rpcClient.RequestClusterLogs(node.Id, &clusterLogsReq{
	// 				StartLogIndex: startLogIndex,
	// 				EndLogIndex:   0,
	// 				Limit:         limit,
	// 				OrderDesc:     true,
	// 				LogType:       logType,
	// 			})
	// 			if err != nil {
	// 				c.ResponseError(err)
	// 				return
	// 			}
	// 			if len(logs) == 0 {
	// 				goto end
	// 			}
	// 			nodeLogs[node.Id] = logs
	// 		}
	// 		if len(nodeLogs) == 0 {
	// 			goto end
	// 		}

	// 		// 比较节点日志，比较日志的term和index和id，如果相同则认为日志一致

	// 		maxLoop--
	// 		if maxLoop == 0 {
	// 			c.ResponseError(fmt.Errorf("max loop over, startLogIndex: %d, maxLoop: %d", startLogIndex, maxLoop))
	// 			return
	// 		}
	// 	}

	// end:
}
