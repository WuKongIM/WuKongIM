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

func NewInt64ObservableCounter(name string) metric.Int64ObservableCounter {
	v, err := meter.Int64ObservableCounter(name)
	if err != nil {
		panic(err)
	}
	return v
}

func NewFloat64ObservableCounter(name string) metric.Float64ObservableCounter {
	v, err := meter.Float64ObservableCounter(name)
	if err != nil {
		panic(err)
	}
	return v

}

func NewFloat64ObservableGauge(name string) metric.Float64ObservableGauge {
	v, err := meter.Float64ObservableGauge(name)
	if err != nil {
		panic(err)
	}
	return v
}

func NewInt64ObservableGauge(name string) metric.Int64ObservableGauge {
	v, err := meter.Int64ObservableGauge(name)
	if err != nil {
		panic(err)
	}
	return v
}

func RegisterCallback(f metric.Callback, instruments ...metric.Observable) {
	_, err := meter.RegisterCallback(f, instruments...)
	if err != nil {
		panic(err)
	}
}
