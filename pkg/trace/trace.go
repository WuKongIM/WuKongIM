package trace

import (
	"context"
	"fmt"
	"net/http"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var GlobalTrace *Trace

func SetGlobalTrace(t *Trace) {
	GlobalTrace = t
}

var (
	tracer = otel.Tracer("trace")
)

type Trace struct {
	opts     *Options
	ctx      context.Context
	shutdown func(context.Context) error

	// Metrics 监控
	Metrics IMetrics
	wklog.Log
}

func New(ctx context.Context, opts *Options) *Trace {
	return &Trace{
		ctx:     ctx,
		opts:    opts,
		Metrics: newMetrics(opts),
		Log:     wklog.NewWKLog("Trace"),
	}
}

func (t *Trace) Start() error {
	shutdown, err := t.setupOTelSDK(t.ctx, t.opts.TraceOn)
	if err != nil {
		return err
	}
	t.shutdown = shutdown
	return nil
}

func (t *Trace) Stop() {
	t.Debug("stop...")
	if !t.opts.TraceOn {
		return
	}
	if t.shutdown != nil {
		err := t.shutdown(t.ctx)
		if err != nil {
			panic(err)
		}
	}
}

func (t *Trace) Handler() http.Handler {
	return promhttp.Handler()
}

func (t *Trace) StartSpan(ctx context.Context, name string) (context.Context, Span) {
	if !t.opts.TraceOn {
		return ctx, emptySpan
	}
	ctx, span := tracer.Start(ctx, name)
	return ctx, defaultSpan{
		Span: span,
	}
}

type Span interface {
	trace.Span
	SetInt(key string, value int)
	SetInt64(key string, value int64)
	SetString(key string, value string)
	SetUint8(key string, value uint8)
	SetUint64(key string, value uint64)
	SetUint32(key string, value uint32)
	SetUint64s(key string, value []uint64)
	SetBool(key string, value bool)
}

var emptySpan = EmptySpan{}

type defaultSpan struct {
	trace.Span
}

func (d defaultSpan) SetInt(key string, value int) {
	d.SetAttributes(attribute.Int(key, value))
}

func (d defaultSpan) SetString(key string, value string) {
	d.SetAttributes(attribute.String(key, value))

}

func (d defaultSpan) SetInt64(key string, value int64) {
	d.SetAttributes(attribute.Int64(key, value))
}

func (d defaultSpan) SetUint8(key string, value uint8) {
	d.SetAttributes(attribute.Int(key, int(value)))
}

func (d defaultSpan) SetUint64(key string, value uint64) {
	d.SetAttributes(attribute.String(key, fmt.Sprintf("%d", value)))
}

func (d defaultSpan) SetUint32(key string, value uint32) {
	d.SetAttributes(attribute.String(key, fmt.Sprintf("%d", value)))
}

func (d defaultSpan) SetUint64s(key string, value []uint64) {
	var str string
	for _, v := range value {
		str += fmt.Sprintf("%d,", v)
	}
	d.SetAttributes(attribute.String(key, str))

}

func (d defaultSpan) SetBool(key string, value bool) {
	d.SetAttributes(attribute.Bool(key, value))

}

func SpanFromContext(ctx context.Context) Span {

	if !GlobalTrace.opts.TraceOn {
		return emptySpan
	}

	span := trace.SpanFromContext(ctx)
	if span == nil {
		return nil
	}

	return defaultSpan{
		Span: span,
	}
}

func ContextWithRemoteSpanContext(ctx context.Context, sc SpanContext) context.Context {
	if !GlobalTrace.opts.TraceOn {
		return context.Background()
	}
	return trace.ContextWithRemoteSpanContext(ctx, sc.SpanContext)

}

func NewSpanContext(cfg SpanContextConfig) SpanContext {
	if !GlobalTrace.opts.TraceOn {
		return emptySpanContext
	}
	return SpanContext{
		SpanContext: trace.NewSpanContext(cfg.SpanContextConfig()),
	}
}

type SpanContext struct {
	trace.SpanContext
}

type SpanContextConfig struct {
	TraceID    TraceID
	SpanID     SpanID
	TraceState TraceState
	Remote     bool
}

func (s SpanContextConfig) SpanContextConfig() trace.SpanContextConfig {
	return trace.SpanContextConfig{
		TraceID:    trace.TraceID(s.TraceID),
		SpanID:     trace.SpanID(s.SpanID),
		TraceFlags: trace.FlagsSampled,
		TraceState: trace.TraceState(s.TraceState),
		Remote:     s.Remote,
	}
}

type TraceID trace.TraceID
type SpanID trace.SpanID

type TraceFlags trace.TraceFlags

type TraceState trace.TraceState
