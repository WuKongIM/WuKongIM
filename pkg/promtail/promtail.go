package promtail

import (
	"fmt"
	"strings"

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

	p, err := ptail.New(cfg, nil, clientMetrics, false)
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

	cfg.PositionsConfig.PositionsFile = fmt.Sprintf("%s/positions.yml", opts.LogDir)

	targetGroup := targetgroup.Group{
		Targets: []model.LabelSet{{
			"address": model.LabelValue(opts.Address),
		}},
		Labels: model.LabelSet{
			"job":      "wukongimlogs",
			"__path__": model.LabelValue(opts.LogDir + "/**/*.log"),
		},
		Source: "",
	}
	scrapeConfig := scrapeconfig.Config{
		JobName: "",
		PipelineStages: stages.PipelineStages{
			stages.PipelineStage{
				stages.StageTypeJSON: stages.JSONConfig{
					Expressions: map[string]string{
						"level":     "level",
						"timestamp": "time",
						"output":    "msg",
					},
				},
			},
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
