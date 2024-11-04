package trace

import (
	"context"
	"errors"
	"log"

	"go.opentelemetry.io/otel"
	prometheusExp "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
)

func (t *Trace) setupOTelSDK(ctx context.Context) (shutdown func(context.Context) error, err error) {
	var shutdownFuncs []func(context.Context) error
	// shutdown 会调用通过 shutdownFuncs 注册的清理函数。
	// 调用产生的错误会被合并。
	// 每个注册的清理函数将被调用一次。
	shutdown = func(ctx context.Context) error {
		var err error
		for _, fn := range shutdownFuncs {
			err = errors.Join(err, fn(ctx))
		}
		shutdownFuncs = nil
		return err
	}
	// handleErr 调用 shutdown 进行清理，并确保返回所有错误信息。
	handleErr := func(inErr error) {
		err = errors.Join(inErr, shutdown(ctx))
	}

	// 设置传播器
	prop := newPropagator()
	otel.SetTextMapPropagator(prop)

	// 设置 trace provider.
	// tracerProvider, err := newTraceProvider()
	// if traceOn {
	// 	var tracerProvider *trace.TracerProvider
	// 	tracerProvider, err = newJaegerTraceProvider(ctx, t.opts.Endpoint, t.opts.ServiceName, t.opts.ServiceHostName, nodeId)
	// 	if err != nil {
	// 		fmt.Println("newJaegerTraceProvider err---->", err)
	// 		handleErr(err)
	// 		return
	// 	}
	// 	shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown)
	// 	otel.SetTracerProvider(tracerProvider)
	// }

	// 设置 meter provider.
	meterProvider, err := newMeterProvider()
	if err != nil {
		handleErr(err)
		return
	}
	shutdownFuncs = append(shutdownFuncs, meterProvider.Shutdown)
	otel.SetMeterProvider(meterProvider)
	return nil, nil
}

func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

// func newJaegerTraceProvider(ctx context.Context, endpoint string, serviceName, serviceHostname string, nodeId uint64) (*trace.TracerProvider, error) {
// 	// 创建一个使用 HTTP 协议连接本机Jaeger的 Exporter
// 	traceExporter, err := otlptracehttp.New(ctx,
// 		otlptracehttp.WithEndpoint(endpoint),
// 		otlptracehttp.WithInsecure())
// 	if err != nil {
// 		return nil, err
// 	}
// 	res, err := resource.New(ctx,
// 		resource.WithFromEnv(),
// 		resource.WithProcess(),
// 		resource.WithTelemetrySDK(),
// 		resource.WithHost(),
// 		resource.WithAttributes(
// 			// 在可观测链路 OpenTelemetry 版后端显示的服务名称。
// 			semconv.ServiceNameKey.String(serviceName),
// 			semconv.HostNameKey.String(serviceHostname),
// 			attribute.Int64("node.id", int64(nodeId)),
// 		),
// 	)
// 	if err != nil {
// 		return nil, err
// 	}
// 	traceProvider := trace.NewTracerProvider(
// 		trace.WithResource(res),
// 		trace.WithSampler(trace.TraceIDRatioBased(1.0)), // 采样率
// 		trace.WithBatcher(traceExporter,
// 			trace.WithBatchTimeout(time.Second*5)),
// 	)
// 	return traceProvider, nil
// }

// func newTraceProvider() (*trace.TracerProvider, error) {
// 	traceExporter, err := stdouttrace.New(
// 		stdouttrace.WithPrettyPrint())
// 	if err != nil {
// 		return nil, err
// 	}

//		traceProvider := trace.NewTracerProvider(
//			trace.WithBatcher(traceExporter,
//				// 默认为 5s。为便于演示，设置为 1s。
//				trace.WithBatchTimeout(time.Second)),
//		)
//		return traceProvider, nil
//	}

func newMeterProvider() (*metric.MeterProvider, error) {

	// 创建exporter和provider
	exporter, err := prometheusExp.New()
	if err != nil {
		log.Fatal(err)
	}

	meterProvider := metric.NewMeterProvider(
		metric.WithReader(exporter),
	)
	return meterProvider, nil
}
