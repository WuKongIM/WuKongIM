package sim

import (
	"encoding/json"
	"fmt"
	"io"
)

func renderHuman(w io.Writer, snapshot Snapshot) error {
	_, err := fmt.Fprintf(
		w,
		"wkcli sim state=%s run=%s users=%d active=%d groups=%d messages=%d send_errors=%d recv=%d reconnects=%d last_error=%q\n",
		snapshot.State,
		snapshot.RunID,
		snapshot.Users,
		snapshot.ActiveUsers,
		snapshot.Groups,
		snapshot.MessagesSent,
		snapshot.SendErrors,
		snapshot.RecvMessages,
		snapshot.Reconnects,
		snapshot.LastError,
	)
	return err
}

func renderJSON(w io.Writer, snapshot Snapshot) error {
	return json.NewEncoder(w).Encode(snapshot)
}
