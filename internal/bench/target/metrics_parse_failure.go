package target

import "errors"

// MetricsParseReason is a closed parser-leaf diagnostic, never metric contents or a verdict.
type MetricsParseReason string

// Bounded discards arbitrary text supplied by an external diagnostic caller.
func (r MetricsParseReason) Bounded() MetricsParseReason {
	if _, known := metricsParseText(r); known {
		return r
	}
	return "unknown"
}

// metricsParseError preserves the original fixed parser text without retaining a sample.
type metricsParseError struct {
	// reason identifies a fixed guard; it must never contain metric contents.
	reason MetricsParseReason
}

func (e *metricsParseError) Error() string {
	if e != nil {
		if text, known := metricsParseText(e.reason); known {
			return text
		}
	}
	return "observation metrics parse"
}

func newMetricsParseError(reason MetricsParseReason) error {
	return &metricsParseError{reason: reason.Bounded()}
}

// MetricsParseFailure inspects only a private typed cause, without formatting errors.
// The caller must separately establish that the acquisition failure kind is parse.
func MetricsParseFailure(err error) MetricsParseReason {
	if err == nil {
		return ""
	}
	var failure *metricsParseError
	if errors.As(err, &failure) && failure != nil {
		return failure.reason.Bounded()
	}
	return "unknown"
}

func metricsParseText(reason MetricsParseReason) (string, bool) {
	switch reason {
	case "line_limit":
		return "observation metrics line limit exceeded", true
	case "series_limit":
		return "observation metrics series cardinality limit exceeded", true
	case "invalid_value":
		return "observation metrics contains invalid value", true
	case "incomplete_metadata_results":
		return "observation metrics contains incomplete metadata-create results", true
	case "invalid_sample":
		return "observation metrics contains invalid sample", true
	case "invalid_labels":
		return "observation metrics contains invalid labels", true
	case "invalid_metadata_labels":
		return "observation metrics contains invalid metadata-create labels", true
	case "invalid_metadata_slot":
		return "observation metrics contains invalid metadata-create Slot", true
	case "invalid_metadata_counter":
		return "observation metrics contains invalid metadata-create counter", true
	case "invalid_metadata_result":
		return "observation metrics contains invalid metadata-create result", true
	case "duplicate_metadata_slot_result":
		return "observation metrics contains duplicate metadata-create Slot result", true
	case "aggregate_overflow":
		return "observation metrics aggregate overflow", true
	case "unsupported_family":
		return "observation metrics contains unsupported family", true
	case "invalid_queue_labels":
		return "observation metrics contains invalid queue labels", true
	case "invalid_queue_family":
		return "observation metrics contains invalid queue family", true
	case "duplicate_or_excess_queue_series":
		return "observation metrics contains duplicate or excessive queue series", true
	case "unpaired_queue_series":
		return "observation metrics contains unpaired queue series", true
	case "invalid_queue_utilization":
		return "observation metrics contains invalid queue utilization", true
	case "invalid_sample_name":
		return "observation metrics contains invalid sample name", true
	case "label_cardinality_limit":
		return "observation metrics label cardinality limit exceeded", true
	default:
		return "", false
	}
}
