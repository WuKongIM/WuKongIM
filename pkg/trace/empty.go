package trace

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
)

type EmptySpan struct {
	embedded.Span
}

func (e EmptySpan) End(options ...trace.SpanEndOption) {

}

func (e EmptySpan) AddEvent(name string, options ...trace.EventOption) {

}

func (e EmptySpan) IsRecording() bool {
	return false
}

func (e EmptySpan) RecordError(err error, options ...trace.EventOption) {

}

func (e EmptySpan) SpanContext() trace.SpanContext {
	return emptySpanContext.SpanContext
}

func (e EmptySpan) SetStatus(code codes.Code, description string) {

}

func (e EmptySpan) SetName(name string) {

}

func (e EmptySpan) SetAttributes(kv ...attribute.KeyValue) {

}

// TracerProvider returns a TracerProvider that can be used to generate
// additional Spans on the same telemetry pipeline as the current Span.
func (e EmptySpan) TracerProvider() trace.TracerProvider {
	return nil
}

func (e EmptySpan) SetInt(key string, value int) {

}
func (e EmptySpan) SetInt64(key string, value int64) {

}
func (e EmptySpan) SetString(key string, value string) {

}
func (e EmptySpan) SetUint8(key string, value uint8) {

}
func (e EmptySpan) SetUint64(key string, value uint64) {

}
func (e EmptySpan) SetUint32(key string, value uint32) {

}
func (e EmptySpan) SetUint64s(key string, value []uint64) {

}
func (e EmptySpan) SetBool(key string, value bool) {

}

func (e EmptySpan) AddLink(link trace.Link) {

}

var emptySpanContext = SpanContext{}
