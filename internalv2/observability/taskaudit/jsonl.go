package taskaudit

import (
	"bufio"
	"encoding/json"
	"io"
)

func encodeJSONL(event Event) ([]byte, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	return data, nil
}

func replayJSONL(r io.Reader, apply func(Event)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*DefaultDetailLimitBytes)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		apply(event)
	}
	return scanner.Err()
}
