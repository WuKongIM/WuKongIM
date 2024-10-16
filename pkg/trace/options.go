package trace

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"go.uber.org/zap"
)

type Options struct {
	NodeId  uint64
	TraceOn bool // 是否开启trace
	// Endpoint is the address of the collector to which the exporter will send the spans.
	Endpoint         string
	ServiceName      string
	ServiceHostName  string
	PrometheusApiUrl string
	ReqTimeout       time.Duration

	prometheusClient api.Client // prometheus client
	prometheusApi    v1.API
}

func NewOptions(opt ...Option) *Options {
	opts := &Options{
		TraceOn:          false,
		Endpoint:         "127.0.0.1:4318",
		ServiceName:      "wukongim",
		ServiceHostName:  "wukongim",
		PrometheusApiUrl: "http://127.0.0.1:9090",
		ReqTimeout:       5 * time.Second,
	}

	for _, o := range opt {
		o(opts)
	}

	return opts
}

type Option func(*Options)

func WithEndpoint(endpoint string) Option {
	return func(o *Options) {
		o.Endpoint = endpoint
	}
}

func WithServiceName(name string) Option {
	return func(o *Options) {
		o.ServiceName = name
	}
}

func WithServiceHostName(name string) Option {
	return func(o *Options) {
		o.ServiceHostName = name
	}
}

func WithPrometheusApiUrl(url string) Option {
	return func(o *Options) {
		o.PrometheusApiUrl = url
	}
}

func WithReqTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		o.ReqTimeout = timeout
	}
}

func WithTraceOn(on bool) Option {
	return func(o *Options) {
		o.TraceOn = on
	}
}

func WithNodeId(id uint64) Option {
	return func(o *Options) {
		o.NodeId = id
	}
}

func (o *Options) requestPrometheus(query string, r v1.Range, opt ...v1.Option) (model.Value, error) {

	if o.prometheusClient == nil {
		cli, err := api.NewClient(api.Config{
			Address: o.PrometheusApiUrl,
		})
		if err != nil {
			wklog.Error("create prometheus client failed", zap.Error(err))
			return nil, err
		}
		o.prometheusClient = cli
		v1api := v1.NewAPI(o.prometheusClient)
		o.prometheusApi = v1api
	}

	ctx, cancel := context.WithTimeout(context.Background(), o.ReqTimeout)
	defer cancel()
	result, warnings, err := o.prometheusApi.QueryRange(ctx, query, r)
	if err != nil {
		wklog.Error("query prometheus failed", zap.Error(err))
		return nil, err
	}
	if len(warnings) > 0 {
		wklog.Warn("query prometheus warnings", zap.Any("warnings", warnings))
	}
	return result, nil
}
