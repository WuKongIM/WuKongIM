package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/network"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type stress struct {
	s *Server
	wklog.Log
}

func newStress(s *Server) *stress {
	return &stress{
		s:   s,
		Log: wklog.NewWKLog("stress"),
	}
}

// Route route
func (s *stress) route(r *wkhttp.WKHttp) {
	r.POST("/stress/add", s.add)            // 添加测试机器
	r.POST("/stress/remove", s.remove)      //移除测试机
	r.POST("/stress/start", s.start)        // 开始压测机
	r.POST("/stress/stop", s.stop)          // 停止压测机
	r.POST("/stress/report", s.report)      // 压测报告
	r.GET("/stress/infoList", s.infoList)   // 压测机信息列表
	r.GET("/stress/templates", s.templates) // 获取所有模板
}

func (s *stress) add(c *wkhttp.Context) {
	var req struct {
		Addr string `json:"addr"` // 压测机地址 格式 http://xxx.xxx.xx.xx:9466
	}
	if err := c.BindJSON(&req); err != nil {
		s.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if !options.G.Stress {
		c.ResponseError(errors.New("没有开启压测配置，不支持压测"))
		return
	}

	no, err := s.requestExchange(req.Addr, options.G.External.APIUrl)
	if err != nil {
		s.Error("请求交换失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	// 保存压测机数据
	nw := time.Now()
	err = service.Store.AddOrUpdateTester(wkdb.Tester{
		No:        no,
		Addr:      req.Addr,
		CreatedAt: &nw,
		UpdatedAt: &nw,
	})
	if err != nil {
		s.Error("保存压测机数据失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.ResponseOK()
}

func (s *stress) remove(c *wkhttp.Context) {
	var req struct {
		No string `json:"no"` // 压测机编号
	}
	if err := c.BindJSON(&req); err != nil {
		s.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	// 移除压测机数据
	err := service.Store.RemoveTester(req.No)
	if err != nil {
		s.Error("移除压测机数据失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.ResponseOK()
}

func (s *stress) infoList(c *wkhttp.Context) {

	leaderInfo := service.Cluster.SlotLeaderNodeInfo(defaultSlotId) // 获取频道的领导节点
	if leaderInfo == nil {
		s.Error("槽领导对应的节点信息不存在！", zap.Uint32("slotId", defaultSlotId))
		c.ResponseError(errors.New("槽领导对应的节点信息不存在！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
	if !leaderIsSelf {
		s.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), nil)
		return
	}

	testers, err := service.Store.GetTesters()
	if err != nil {
		s.Error("获取压测机列表失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	g, _ := errgroup.WithContext(timeoutCtx)

	var (
		respList     = make([]*testerInfoResp, 0)
		respListLock sync.Mutex
	)

	for _, tester := range testers {
		tester := tester
		g.Go(func() error {
			resp, err := s.requestSystemInfo(tester.Addr)
			if err != nil {
				s.Error("请求压测机系统消息信息失败！", zap.Error(err), zap.String("no", tester.No))
				resp = newTesterInfoResp(tester)
				resp.Status = testerStatusErr
			}

			respListLock.Lock()
			respList = append(respList, resp)
			respListLock.Unlock()
			return nil
		})
	}
	_ = g.Wait()

	c.JSON(http.StatusOK, respList)
}

func (s *stress) start(c *wkhttp.Context) {
	var req struct {
		taskCfg
		No string `json:"no"` // 机器编号
	}
	if err := c.BindJSON(&req); err != nil {
		s.Error("start: 请求数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	tester, err := service.Store.GetTester(req.No)
	if err != nil {
		s.Error("get tester failed", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if tester.No == "" {
		s.Error("压测机器不存在！", zap.String("no", req.No))
		c.ResponseError(errors.New("压测机器不存在！"))
		return
	}

	// 请求开始压测
	err = s.requestStart(tester.Addr, &req.taskCfg)
	if err != nil {
		s.Error("请求压测机失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.ResponseOK()
}

func (s *stress) stop(c *wkhttp.Context) {
	var req struct {
		No string `json:"no"`
	}
	if err := c.BindJSON(&req); err != nil {
		s.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	tester, err := service.Store.GetTester(req.No)
	if err != nil {
		s.Error("get tester failed", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if tester.No == "" {
		s.Error("压测机器不存在！", zap.String("no", req.No))
		c.ResponseError(errors.New("压测机器不存在！"))
		return
	}

	// 请求停止压测
	err = s.requestStop(tester.Addr)
	if err != nil {
		s.Error("请求压测机失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.ResponseOK()

}

func (s *stress) report(c *wkhttp.Context) {
	var req struct {
		No string `json:"no"`
	}
	if err := c.BindJSON(&req); err != nil {
		s.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	tester, err := service.Store.GetTester(req.No)
	if err != nil {
		s.Error("get tester failed", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if tester.No == "" {
		s.Error("压测机器不存在！", zap.String("no", req.No))
		c.ResponseError(errors.New("压测机器不存在！"))
		return
	}

	stats, err := s.requestReport(tester.Addr)
	if err != nil {
		s.Error("请求压测机失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	c.JSON(http.StatusOK, stats)
}

// 获取模块
func (s *stress) templates(c *wkhttp.Context) {
	templates := []template{
		{
			Name: "正常聊天",
			Task: &taskCfg{
				Online: 10000,
				Channels: []*channelCfg{
					{
						Count: 200,
						Type:  2,
						Subscriber: &subscriberCfg{
							Count:  200,
							Online: 50,
						},
						MsgRate: 30,
					},
					{
						Count: 100,
						Type:  2,
						Subscriber: &subscriberCfg{
							Count:  500,
							Online: 125,
						},
						MsgRate: 30,
					},
					{
						Count: 1,
						Type:  2,
						Subscriber: &subscriberCfg{
							Count:  1000,
							Online: 250,
						},
						MsgRate: 30,
					},
				},
				P2p: &p2pCfg{
					Count:   1000,
					MsgRate: 30,
				},
			},
		},
		{
			Name: "万人群测试",
			Task: &taskCfg{
				Online: 3000,
				Channels: []*channelCfg{
					{
						Count: 10,
						Type:  2,
						Subscriber: &subscriberCfg{
							Count:  10000,
							Online: 1000,
						},
						MsgRate: 60,
					},
				},
				P2p: &p2pCfg{
					Count:   0,
					MsgRate: 0,
				},
			},
		},
		{
			Name: "高强度发送（4核cpu以上）",
			Task: &taskCfg{
				Online: 1000,
				P2p: &p2pCfg{
					Count:   1000,
					MsgRate: 300,
				},
			},
		},
	}

	c.JSON(http.StatusOK, templates)
}

// 请求压测机交互数据
func (s *stress) requestExchange(addr string, server string) (string, error) {

	resp, err := network.Post(strings.TrimSuffix(addr, "/")+"/v1/exchange", []byte(wkutil.ToJSON(map[string]interface{}{
		"server": server,
	})), nil)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("request exchange failed")
	}

	var resultMap map[string]interface{}
	if err := wkutil.ReadJSONByByte([]byte(resp.Body), &resultMap); err != nil {
		return "", err
	}
	return resultMap["id"].(string), nil
}

// 请求压测机系统信息
func (s *stress) requestSystemInfo(addr string) (*testerInfoResp, error) {
	resp, err := network.Get(strings.TrimSuffix(addr, "/")+"/v1/systemInfo", nil, nil)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		s.Error("requestSystemInfo failed", zap.Error(err), zap.String("addr", addr))
		return nil, errors.New("requestSystemInfo failed")
	}

	var testerResp *testerInfoResp
	if err := wkutil.ReadJSONByByte([]byte(resp.Body), &testerResp); err != nil {
		return nil, err
	}

	return testerResp, nil
}

// 请求压测机开始压测
func (s *stress) requestStart(addr string, task *taskCfg) error {
	resp, err := network.Post(strings.TrimSuffix(addr, "/")+"/v1/stress/start", []byte(wkutil.ToJSON(task)), nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		s.Error("请求压测机失败！", zap.Int("status", resp.StatusCode))
		return errors.New("请求压测机失败！")
	}
	return nil
}

// 请求压测机停止压测
func (s *stress) requestStop(addr string) error {
	resp, err := network.Post(strings.TrimSuffix(addr, "/")+"/v1/stress/stop", nil, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		s.Error("请求压测机失败！", zap.Int("status", resp.StatusCode))
		return errors.New("请求压测机失败！")
	}
	return nil
}

// 请求压测机报告
func (s *stress) requestReport(addr string) (*statsResp, error) {
	resp, err := network.Get(strings.TrimSuffix(addr, "/")+"/v1/stress/report", nil, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		s.Error("请求压测机失败！", zap.Int("status", resp.StatusCode))
		return nil, errors.New("请求压测机失败！")
	}

	var stats *statsResp
	if err := wkutil.ReadJSONByByte([]byte(resp.Body), &stats); err != nil {
		return nil, err
	}

	return stats, nil
}

type testerInfoResp struct {
	No                string       `json:"no"`                  // 压测机编号
	Addr              string       `json:"addr"`                // 压测机地址
	CpuPercent        float32      `json:"cpu_percent"`         // cpu使用率
	CpuPercentDesc    string       `json:"cpu_percent_desc"`    // cpu使用率描述
	CpuCore           int          `json:"cpu_core"`            // cpu核心数
	MemoryTotal       int          `json:"memory_total"`        // 总内存数量(单位byte)
	MemoryTotalDesc   string       `json:"memory_total_desc"`   // 总内存描述
	MemoryFree        int          `json:"memory_free"`         // 空闲内存数量(单位byte)
	MemoryFreeDesc    string       `json:"memory_free_desc"`    // 空闲内存描述
	MemoryUsed        int          `json:"memory_used"`         // 已使用内存数量(单位byte)
	MemoryUsedDesc    string       `json:"memory_used_desc"`    // 已使用内存描述
	MemoryPercent     float64      `json:"memory_percent"`      // 内存使用率
	MemoryPercentDesc string       `json:"memory_percent_desc"` // 内存使用率描述
	Desc              string       `json:"desc"`
	Status            testerStatus `json:"status"`
	Task              taskCfg      `json:"task"`
	TaskDuration      int64        `json:"task_duration"`      // 任务时间
	TaskDurationDesc  string       `json:"task_duration_desc"` // 任务时间描述
}

func newTesterInfoResp(t wkdb.Tester) *testerInfoResp {
	return &testerInfoResp{
		No:   t.No,
		Addr: t.Addr,
	}
}

type testerStatus int

const (
	// 未知状态
	// testerStatusUnknow testerStatus = 0
	// 正常
	// testerStatusNormal testerStatus = 1
	// 运行中
	// testerStatusRunning testerStatus = 2
	// 错误
	testerStatusErr testerStatus = 3
)

type taskCfg struct {
	Online        int           `json:"online"`         // 在线人数
	Channels      []*channelCfg `json:"channels"`       // 频道（群）聊天设置
	P2p           *p2pCfg       `json:"p2p"`            // 私聊设置
	UserPrefix    string        `json:"user_prefix"`    // 用户前缀
	ChannelPrefix string        `json:"channel_prefix"` // 频道前缀
}

func (t *taskCfg) check() error {
	if t.Online <= 0 {
		return errors.New("online must be greater than 0")
	}

	return nil
}

type channelCfg struct {
	Count      int            `json:"count"`      //  创建频道数
	Type       int            `json:"type"`       //  频道类型
	Subscriber *subscriberCfg `json:"subscriber"` //  频道成员设置
	MsgRate    int            `json:"msg_rate"`   //  每个频道消息发送速率 每分钟条数
}

type subscriberCfg struct {
	Count  int `json:"count"`  //  每个频道的成员数量
	Online int `json:"online"` //  成员在线数量
}

type p2pCfg struct {
	Count   int `json:"count"`    //  私聊数量
	MsgRate int `json:"msg_rate"` //  每个私聊消息发送速率 每分钟条数
}

type statsResp struct {
	Online         int64 `json:"online"`           // 在线人数
	Offline        int64 `json:"offline"`          // 离线人数
	Send           int64 `json:"send"`             // 发送消息数
	SendRate       int64 `json:"send_rate"`        // 发送消息速率 (条/秒)
	SendSuccess    int64 `json:"send_success"`     // 发送成功数
	SendErr        int64 `json:"send_err"`         // 发送失败数
	SendBytes      int64 `json:"send_bytes"`       // 发送字节数
	SendBytesRate  int64 `json:"send_bytes_rate"`  // 发送字节速率 (字节/秒)
	SendMinLatency int64 `json:"send_min_latency"` // 发送最小延迟 (毫秒)
	SendMaxLatency int64 `json:"send_max_latency"` // 发送最大延迟 (毫秒)
	SendAvgLatency int64 `json:"send_avg_latency"` // 发送平均延迟 (毫秒)

	Recv           int64 `json:"recv"`             // 接收消息数
	ExpectRecv     int64 `json:"expect_recv"`      // 预期接收消息数
	RecvRate       int64 `json:"recv_rate"`        // 接收消息速率 (条/秒)
	RecvBytes      int64 `json:"recv_bytes"`       // 接收字节数
	RecvBytesRate  int64 `json:"recv_bytes_rate"`  // 接收字节速率 (字节/秒)
	RecvMinLatency int64 `json:"recv_min_latency"` // 接收最小延迟 (毫秒)
	RecvMaxLatency int64 `json:"recv_max_latency"` // 接收最大延迟 (毫秒)
	RecvAvgLatency int64 `json:"recv_avg_latency"` // 接收平均延迟 (毫秒)

	TaskDuration     int64  `json:"task_duration"`      // 任务时间
	TaskDurationDesc string `json:"task_duration_desc"` // 任务时间描述
}

type template struct {
	Name string   `json:"name"`
	Task *taskCfg `json:"task"`
}

var defaultSlotId uint32 = 0
