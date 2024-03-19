package trace

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var (
	tracer  = otel.Tracer("trace")
	meter   = otel.Meter("trace")
	rollCnt metric.Int64Counter
)

type Trace struct {
	opts     *Options
	ctx      context.Context
	shutdown func(context.Context) error
}

func New(ctx context.Context, opts *Options) *Trace {
	return &Trace{
		ctx:  ctx,
		opts: opts,
	}
}

func (t *Trace) Start() error {
	shutdown, err := t.setupOTelSDK(t.ctx)
	if err != nil {
		return err
	}
	t.shutdown = shutdown
	return nil
}

func (t *Trace) Stop() {
	if t.shutdown != nil {
		err := t.shutdown(t.ctx)
		if err != nil {
			panic(err)
		}
	}
}

func (t *Trace) StartSpan(ctx context.Context, name string) (context.Context, Span) {
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
}

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

func SpanFromContext(ctx context.Context) Span {
	span := trace.SpanFromContext(ctx)
	if span == nil {
		return nil
	}

	return defaultSpan{
		Span: span,
	}
}

func ContextWithRemoteSpanContext(ctx context.Context, sc SpanContext) context.Context {
	return trace.ContextWithRemoteSpanContext(ctx, sc.SpanContext)

}

func NewSpanContext(cfg SpanContextConfig) SpanContext {
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
	TraceFlags TraceFlags
	TraceState TraceState
	Remote     bool
}

func (s SpanContextConfig) SpanContextConfig() trace.SpanContextConfig {
	return trace.SpanContextConfig{
		TraceID:    trace.TraceID(s.TraceID),
		SpanID:     trace.SpanID(s.SpanID),
		TraceFlags: trace.TraceFlags(s.TraceFlags),
		TraceState: trace.TraceState(s.TraceState),
		Remote:     s.Remote,
	}
}

type TraceID trace.TraceID
type SpanID trace.SpanID

type TraceFlags trace.TraceFlags

type TraceState trace.TraceState
