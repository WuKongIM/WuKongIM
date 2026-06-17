package app

import (
	"context"

	applog "github.com/WuKongIM/WuKongIM/internalv2/log"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

// applicationLogReader adapts node-local app log DTOs to manager usecase DTOs.
type applicationLogReader struct {
	// nodeID is the local node whose ordinary application logs are read.
	nodeID uint64
	// reader performs bounded reads from fixed node-local log files.
	reader *applog.AppLogReader
}

func newApplicationLogReader(nodeID uint64, dir string) *applicationLogReader {
	return &applicationLogReader{
		nodeID: nodeID,
		reader: applog.NewAppLogReader(applog.AppLogReaderOptions{Dir: dir}),
	}
}

// ApplicationLogSources returns the ordinary application log sources available on this node.
func (r *applicationLogReader) ApplicationLogSources(ctx context.Context, req managementusecase.ApplicationLogSourcesRequest) (managementusecase.ApplicationLogSourcesResponse, error) {
	if r == nil || r.reader == nil {
		return managementusecase.ApplicationLogSourcesResponse{}, managementusecase.ErrApplicationLogReaderUnavailable
	}
	resp, err := r.reader.Sources(ctx, applog.AppLogSourcesRequest{NodeID: r.nodeID})
	if err != nil {
		return managementusecase.ApplicationLogSourcesResponse{}, err
	}
	out := managementusecase.ApplicationLogSourcesResponse{
		NodeID:  req.NodeID,
		Sources: make([]managementusecase.ApplicationLogSource, 0, len(resp.Sources)),
	}
	for _, source := range resp.Sources {
		out.Sources = append(out.Sources, managementusecase.ApplicationLogSource{
			Name:       source.Name,
			File:       source.File,
			Available:  source.Available,
			SizeBytes:  source.SizeBytes,
			ModifiedAt: source.ModifiedAt,
		})
	}
	return out, nil
}

// ApplicationLogEntries returns one page from a local ordinary application log source.
func (r *applicationLogReader) ApplicationLogEntries(ctx context.Context, req managementusecase.ApplicationLogEntriesRequest) (managementusecase.ApplicationLogEntriesResponse, error) {
	if r == nil || r.reader == nil {
		return managementusecase.ApplicationLogEntriesResponse{}, managementusecase.ErrApplicationLogReaderUnavailable
	}
	resp, err := r.reader.Entries(ctx, applog.AppLogEntriesRequest{
		NodeID:  r.nodeID,
		Source:  req.Source,
		Limit:   req.Limit,
		Cursor:  req.Cursor,
		Keyword: req.Keyword,
		Levels:  req.Levels,
	})
	if err != nil {
		return managementusecase.ApplicationLogEntriesResponse{}, err
	}
	out := managementusecase.ApplicationLogEntriesResponse{
		NodeID:  req.NodeID,
		Source:  resp.Source,
		Cursor:  resp.Cursor,
		Rotated: resp.Rotated,
		Items:   make([]managementusecase.ApplicationLogEntry, 0, len(resp.Items)),
	}
	for _, item := range resp.Items {
		out.Items = append(out.Items, managementusecase.ApplicationLogEntry{
			Seq:       item.Seq,
			Offset:    item.Offset,
			Time:      item.Time,
			Level:     item.Level,
			Module:    item.Module,
			Caller:    item.Caller,
			Message:   item.Message,
			Fields:    cloneApplicationLogFields(item.Fields),
			Raw:       item.Raw,
			Truncated: item.Truncated,
		})
	}
	return out, nil
}

func cloneApplicationLogFields(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
