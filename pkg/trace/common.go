package trace

import "go.opentelemetry.io/otel/metric"

func NewInt64Counter(name string) metric.Int64Counter {
	v, err := meter.Int64Counter(name)
	if err != nil {
		panic(err)
	}
	return v
}

func NewInt64UpDownCounter(name string) metric.Int64UpDownCounter {
	v, err := meter.Int64UpDownCounter(name)
	if err != nil {
		panic(err)
	}
	return v
}
