package promtail

import (
	"fmt"
	"os"
	"strings"

	"github.com/go-kit/log"
	"github.com/grafana/dskit/flagext"
	"github.com/grafana/loki/v3/clients/pkg/logentry/stages"
	ptail "github.com/grafana/loki/v3/clients/pkg/promtail"
	"github.com/grafana/loki/v3/clients/pkg/promtail/client"
	"github.com/grafana/loki/v3/clients/pkg/promtail/config"
	"github.com/grafana/loki/v3/clients/pkg/promtail/scrapeconfig"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/discovery"
	"github.com/prometheus/prometheus/discovery/targetgroup"
)

type Promtail struct {
	opts *Options
	p    *ptail.Promtail
}

func New(opts *Options) *Promtail {

	var clientMetrics = client.NewMetrics(prometheus.DefaultRegisterer)

	cfg := initPromtailConfig(opts)

	p, err := ptail.New(cfg, nil, clientMetrics, false, ptail.WithLogger(log.NewLogfmtLogger(os.Stdout)))
	if err != nil {
		panic(fmt.Errorf("promtail: Failed to create promtail %s", err))
	}

	return &Promtail{
		opts: opts,
		p:    p,
	}
}

func (p *Promtail) Start() error {

	go func() {
		err := p.p.Run()
		if err != nil {
			panic(fmt.Errorf("promtail: Failed to run promtail %s", err))
		}
	}()

	return nil
}

func (p *Promtail) Stop() {
	p.p.Shutdown()
}

func initPromtailConfig(opts *Options) config.Config {

	var clientURL flagext.URLValue
	err := clientURL.Set(fmt.Sprintf("%s/loki/api/v1/push", strings.TrimSuffix(opts.Url, "/")))
	if err != nil {
		panic(fmt.Errorf("promtail: Failed to parse client URL  %s", err))
	}

	cfg := config.Config{}
	// 初始化配置
	flagext.RegisterFlags(&cfg)
	const hostname = "localhost" // 不需要暴露 所以这里使用localhost就行
	cfg.ServerConfig.HTTPListenAddress = hostname
	cfg.ServerConfig.ExternalURL = hostname
	cfg.ServerConfig.GRPCListenAddress = hostname
	// 不需要暴露，所以这里填0 表示随机端口
	cfg.ServerConfig.HTTPListenPort = 0
	cfg.ServerConfig.GRPCListenPort = 0

	cfg.ClientConfig.URL = clientURL
	cfg.ClientConfig.BatchWait = opts.BatchWait
	cfg.ClientConfig.BatchSize = opts.BatchBytes

	posDir := fmt.Sprintf("%s/loki", opts.LogDir)

	if err := os.MkdirAll(posDir, os.ModePerm); err != nil {
		panic(fmt.Errorf("promtail: Failed to create positions directory %s", err))
	}

	cfg.PositionsConfig.PositionsFile = fmt.Sprintf("%s/positions.yml", posDir)

	targetGroup := targetgroup.Group{
		Targets: []model.LabelSet{{
			"address": model.LabelValue(opts.Address),
			"nodeId":  model.LabelValue(fmt.Sprintf("%d", opts.NodeId)),
		}},
		Labels: model.LabelSet{
			"job":      "wk",
			"__path__": model.LabelValue(opts.LogDir + "/**/*.log"),
		},
		Source: "",
	}

	// 获取本地时区
	// now := time.Now()
	// _, offset := now.Zone()
	// offsetHours := offset / 3600
	// offsetMinutes := (offset % 3600) / 60
	// fmt.Println("Time Zone Name:", name)

	// 格式化偏移量
	// offsetStr := fmt.Sprintf("%-03d:%02d", offsetHours, offsetMinutes)

	// fmt.Println("offsetStr----->", offsetStr)

	// skip := stages.TimestampActionOnFailureSkip
	scrapeConfig := scrapeconfig.Config{
		JobName: "",
		PipelineStages: stages.PipelineStages{
			stages.PipelineStage{
				stages.StageTypeJSON: stages.JSONConfig{
					Expressions: map[string]string{
						"level":  "level",
						"time":   "time",
						"msg":    "msg",
						"trace":  "trace",
						"action": "action",
					},
				},
			},
			stages.PipelineStage{
				stages.StageTypeTimestamp: stages.TimestampConfig{
					Source: "time",
					Format: "RFC3339Nano",
					// ActionOnFailure: &skip,
				},
			},
			// stages.PipelineStage{
			// 	// 为了让loki不再解析json获取以下指标，这里我们推送给loki的日志就解析出来了，这样loki就不需要再解析了 提升性能
			// 	stages.StageTypeStructuredMetadata: stages.LabelsConfig{
			// 		"level": nil,
			// 	},
			// },
			stages.PipelineStage{
				stages.StageTypeLabel: stages.LabelsConfig{
					"level":  nil,
					"trace":  nil,
					"action": nil,
				},
			},

			// stages.PipelineStage{
			// 	stages.StageTypeOutput: stages.OutputConfig{
			// 		Source: "msg",
			// 	},
			// },
		},
		RelabelConfigs: nil,
		ServiceDiscoveryConfig: scrapeconfig.ServiceDiscoveryConfig{
			StaticConfigs: discovery.StaticConfig{
				&targetGroup,
			},
		},
	}
	cfg.ScrapeConfig = append(cfg.ScrapeConfig, scrapeConfig)

	// cfg.TargetConfig.SyncPeriod = 5 * time.Second

	return cfg
}
