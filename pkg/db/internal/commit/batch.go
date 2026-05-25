package commit

type requestBatch struct {
	requests []pendingRequest
	closed   bool
}

func (b requestBatch) stats() (requests int, records int, bytes int) {
	requests = len(b.requests)
	for _, req := range b.requests {
		records += req.Records
		bytes += req.Bytes
	}
	return requests, records, bytes
}

func (b requestBatch) completeAll(err error) {
	for _, req := range b.requests {
		req.done <- err
	}
}
