package chatlifecycle

import (
	"bufio"
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	maxNodeExporterResponseBytes int64 = 8 << 20
	maxNodeExporterLines               = 100_000
	maxNodeExporterLineBytes           = 64 << 10
)

var (
	// ErrDiskMissing means the exact declared filesystem did not expose both required series.
	ErrDiskMissing = errors.New("declared data filesystem metrics are missing")
	// ErrDiskAmbiguous means multiple series matched the exact declared filesystem identity.
	ErrDiskAmbiguous = errors.New("declared data filesystem metrics are ambiguous")
)

// DataFilesystem is the exact node_exporter size/availability pair selected for one data mount.
type DataFilesystem struct {
	// SizeBytes is node_exporter's exact filesystem size observation.
	SizeBytes int64
	// AvailableBytes is node_exporter's exact non-root available-space observation.
	AvailableBytes int64
}

type nodeExporterDiskReader struct {
	endpoint EndpointDeclaration
	client   *http.Client
}

func newNodeExporterDiskReader(endpoint EndpointDeclaration, client *http.Client) *nodeExporterDiskReader {
	return &nodeExporterDiskReader{endpoint: endpoint, client: client}
}

func (r *nodeExporterDiskReader) Filesystem(ctx context.Context) (DataFilesystem, error) {
	metricsURL, err := nodeExporterMetricsURL(r.endpoint.Address)
	if err != nil {
		return DataFilesystem{}, ErrDiskMissing
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, metricsURL, nil)
	if err != nil {
		return DataFilesystem{}, ErrDiskMissing
	}
	response, err := r.client.Do(request)
	if err != nil {
		return DataFilesystem{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return DataFilesystem{}, ErrDiskMissing
	}
	limited := &io.LimitedReader{R: response.Body, N: maxNodeExporterResponseBytes + 1}
	filesystem, err := parseNodeExporterFilesystem(limited, r.endpoint.Mountpoint, r.endpoint.Device)
	if err != nil {
		return DataFilesystem{}, err
	}
	if limited.N <= 0 {
		return DataFilesystem{}, ErrDiskMissing
	}
	return filesystem, nil
}

func nodeExporterMetricsURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/metrics"
	}
	return parsed.String(), nil
}

func parseNodeExporterFilesystem(reader io.Reader, mountpoint, device string) (DataFilesystem, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxNodeExporterLineBytes)
	var filesystem DataFilesystem
	sizeMatches, availableMatches := 0, 0
	for lineCount := 0; scanner.Scan(); lineCount++ {
		if lineCount >= maxNodeExporterLines {
			return DataFilesystem{}, ErrDiskMissing
		}
		line := scanner.Text()
		metric := ""
		switch {
		case strings.HasPrefix(line, "node_filesystem_size_bytes{"):
			metric = "size"
		case strings.HasPrefix(line, "node_filesystem_avail_bytes{"):
			metric = "available"
		default:
			continue
		}
		labels, value, ok := parseNodeExporterSample(line)
		if !ok || labels["mountpoint"] != mountpoint || labels["device"] != device {
			continue
		}
		switch metric {
		case "size":
			sizeMatches++
			filesystem.SizeBytes = value
		case "available":
			availableMatches++
			filesystem.AvailableBytes = value
		}
		if sizeMatches > 1 || availableMatches > 1 {
			return DataFilesystem{}, ErrDiskAmbiguous
		}
	}
	if scanner.Err() != nil {
		return DataFilesystem{}, ErrDiskMissing
	}
	if sizeMatches != 1 || availableMatches != 1 {
		return DataFilesystem{}, ErrDiskMissing
	}
	return filesystem, nil
}

func parseNodeExporterSample(line string) (map[string]string, int64, bool) {
	closeIndex := strings.LastIndexByte(line, '}')
	openIndex := strings.IndexByte(line, '{')
	if openIndex < 0 || closeIndex <= openIndex {
		return nil, 0, false
	}
	labels, ok := parsePrometheusLabels(line[openIndex+1 : closeIndex])
	if !ok {
		return nil, 0, false
	}
	fields := strings.Fields(line[closeIndex+1:])
	if len(fields) != 1 {
		return nil, 0, false
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > math.MaxInt64 {
		return nil, 0, false
	}
	integer := int64(value)
	if float64(integer) != value {
		return nil, 0, false
	}
	return labels, integer, true
}

func parsePrometheusLabels(raw string) (map[string]string, bool) {
	labels := make(map[string]string)
	for index := 0; index < len(raw); {
		keyStart := index
		for index < len(raw) && raw[index] != '=' {
			index++
		}
		if index == keyStart || index >= len(raw) || index+1 >= len(raw) || raw[index+1] != '"' {
			return nil, false
		}
		key := raw[keyStart:index]
		index += 2
		valueStart := index - 1
		escaped := false
		for index < len(raw) {
			if raw[index] == '"' && !escaped {
				break
			}
			if raw[index] == '\\' && !escaped {
				escaped = true
			} else {
				escaped = false
			}
			index++
		}
		if index >= len(raw) {
			return nil, false
		}
		quoted := raw[valueStart : index+1]
		value, err := strconv.Unquote(quoted)
		if err != nil {
			return nil, false
		}
		if _, duplicate := labels[key]; duplicate {
			return nil, false
		}
		labels[key] = value
		index++
		if index == len(raw) {
			break
		}
		if raw[index] != ',' {
			return nil, false
		}
		index++
	}
	return labels, true
}
