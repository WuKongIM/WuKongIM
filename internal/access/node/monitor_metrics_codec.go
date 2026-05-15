package node

import (
	"fmt"
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/metrics"
)

var (
	monitorMetricsRequestMagic  = [...]byte{'W', 'K', 'M', 'Q', 1}
	monitorMetricsResponseMagic = [...]byte{'W', 'K', 'M', 'R', 1}
)

const maxMonitorMetricPoints = 720

func encodeMonitorMetricsRequestBinary(req monitorMetricsRequest) ([]byte, error) {
	dst := make([]byte, 0, len(monitorMetricsRequestMagic)+20)
	dst = append(dst, monitorMetricsRequestMagic[:]...)
	dst = appendUvarint(dst, uint64(req.WindowSeconds))
	dst = appendUvarint(dst, uint64(req.StepSeconds))
	return dst, nil
}

func decodeMonitorMetricsRequest(body []byte) (monitorMetricsRequest, error) {
	if !hasMagic(body, monitorMetricsRequestMagic[:]) {
		return monitorMetricsRequest{}, fmt.Errorf("access/node: invalid monitor metrics request codec")
	}
	offset := len(monitorMetricsRequestMagic)
	window, next, err := readUvarint(body, offset)
	if err != nil {
		return monitorMetricsRequest{}, err
	}
	step, next, err := readUvarint(body, next)
	if err != nil {
		return monitorMetricsRequest{}, err
	}
	if next != len(body) {
		return monitorMetricsRequest{}, fmt.Errorf("access/node: trailing monitor metrics request bytes")
	}
	return monitorMetricsRequest{WindowSeconds: int(window), StepSeconds: int(step)}, nil
}

func encodeMonitorMetricsResponseBinary(resp monitorMetricsResponse) ([]byte, error) {
	dst := make([]byte, 0, len(monitorMetricsResponseMagic)+256)
	dst = append(dst, monitorMetricsResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendMonitorQueryResult(dst, resp.Result)
	return dst, nil
}

func decodeMonitorMetricsResponseBinary(body []byte) (monitorMetricsResponse, error) {
	if !hasMagic(body, monitorMetricsResponseMagic[:]) {
		return monitorMetricsResponse{}, fmt.Errorf("access/node: invalid monitor metrics response codec")
	}
	offset := len(monitorMetricsResponseMagic)
	status, next, err := readString(body, offset)
	if err != nil {
		return monitorMetricsResponse{}, err
	}
	result, next, err := readMonitorQueryResult(body, next)
	if err != nil {
		return monitorMetricsResponse{}, err
	}
	if next != len(body) {
		return monitorMetricsResponse{}, fmt.Errorf("access/node: trailing monitor metrics response bytes")
	}
	return monitorMetricsResponse{Status: status, Result: result}, nil
}

func appendMonitorQueryResult(dst []byte, result metrics.QueryResult) []byte {
	dst = appendUvarint(dst, uint64(result.GeneratedAt.UnixNano()))
	dst = appendUvarint(dst, uint64(result.WindowSeconds))
	dst = appendUvarint(dst, uint64(result.StepSeconds))
	dst = appendUvarint(dst, uint64(result.Points))
	for _, series := range monitorQueryResultSeries(result) {
		dst = appendMonitorMetricSeries(dst, series)
	}
	return dst
}

func readMonitorQueryResult(body []byte, offset int) (metrics.QueryResult, int, error) {
	generatedAt, next, err := readUvarint(body, offset)
	if err != nil {
		return metrics.QueryResult{}, offset, err
	}
	window, next, err := readUvarint(body, next)
	if err != nil {
		return metrics.QueryResult{}, offset, err
	}
	step, next, err := readUvarint(body, next)
	if err != nil {
		return metrics.QueryResult{}, offset, err
	}
	points, next, err := readUvarint(body, next)
	if err != nil {
		return metrics.QueryResult{}, offset, err
	}
	result := metrics.QueryResult{
		GeneratedAt:   time.Unix(0, int64(generatedAt)).UTC(),
		WindowSeconds: int(window),
		StepSeconds:   int(step),
		Points:        int(points),
	}
	targets := monitorQueryResultSeriesRefs(&result)
	for _, target := range targets {
		*target, next, err = readMonitorMetricSeries(body, next)
		if err != nil {
			return metrics.QueryResult{}, offset, err
		}
	}
	return result, next, nil
}

func monitorQueryResultSeries(result metrics.QueryResult) []metrics.MetricSeries {
	return []metrics.MetricSeries{
		result.SendPerSec,
		result.DeliverPerSec,
		result.Connections,
		result.SendLatencyP99Ms,
		result.DeliveryLatencyP99Ms,
		result.SendFailRatePercent,
		result.DeliveryFailRatePercent,
		result.ActiveChannels,
		result.RetryQueueDepth,
		result.FanOutRate,
		result.SendDelta,
		result.DeliverDelta,
		result.SendTotalDelta,
		result.SendFailDelta,
		result.DeliverTotalDelta,
		result.DeliverFailDelta,
		result.ResolveRoutesDelta,
	}
}

func monitorQueryResultSeriesRefs(result *metrics.QueryResult) []*metrics.MetricSeries {
	return []*metrics.MetricSeries{
		&result.SendPerSec,
		&result.DeliverPerSec,
		&result.Connections,
		&result.SendLatencyP99Ms,
		&result.DeliveryLatencyP99Ms,
		&result.SendFailRatePercent,
		&result.DeliveryFailRatePercent,
		&result.ActiveChannels,
		&result.RetryQueueDepth,
		&result.FanOutRate,
		&result.SendDelta,
		&result.DeliverDelta,
		&result.SendTotalDelta,
		&result.SendFailDelta,
		&result.DeliverTotalDelta,
		&result.DeliverFailDelta,
		&result.ResolveRoutesDelta,
	}
}

func appendMonitorMetricSeries(dst []byte, series metrics.MetricSeries) []byte {
	dst = appendUvarint(dst, math.Float64bits(series.Latest))
	dst = appendUvarint(dst, math.Float64bits(series.Peak))
	dst = appendUvarint(dst, math.Float64bits(series.Avg))
	dst = appendUvarint(dst, uint64(len(series.Series)))
	for _, value := range series.Series {
		dst = appendUvarint(dst, math.Float64bits(value))
	}
	return dst
}

func readMonitorMetricSeries(body []byte, offset int) (metrics.MetricSeries, int, error) {
	latest, next, err := readUvarint(body, offset)
	if err != nil {
		return metrics.MetricSeries{}, offset, err
	}
	peak, next, err := readUvarint(body, next)
	if err != nil {
		return metrics.MetricSeries{}, offset, err
	}
	avg, next, err := readUvarint(body, next)
	if err != nil {
		return metrics.MetricSeries{}, offset, err
	}
	count, next, err := readUvarint(body, next)
	if err != nil {
		return metrics.MetricSeries{}, offset, err
	}
	if count > maxMonitorMetricPoints {
		return metrics.MetricSeries{}, offset, fmt.Errorf("access/node: monitor metrics series too large")
	}
	series := make([]float64, int(count))
	for i := range series {
		var bits uint64
		bits, next, err = readUvarint(body, next)
		if err != nil {
			return metrics.MetricSeries{}, offset, err
		}
		series[i] = math.Float64frombits(bits)
	}
	return metrics.MetricSeries{
		Latest: math.Float64frombits(latest),
		Peak:   math.Float64frombits(peak),
		Avg:    math.Float64frombits(avg),
		Series: series,
	}, next, nil
}
