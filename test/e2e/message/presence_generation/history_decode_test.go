//go:build e2e

package presence_generation

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// The actual public response uses an integer above float64's exact range and
// an independently encoded decimal string. Neither field may repair the other.
func TestHistoryMessageIdentityKeepsExactNumericAndStringForms(t *testing.T) {
	var message messageFact
	require.NoError(t, json.Unmarshal([]byte(`{"message_id":2115703436879081472,"message_idstr":"2115703436879081472"}`), &message))
	require.NoError(t, message.validateIdentity())
	require.Equal(t, int64(2115703436879081472), message.ID)
	require.Equal(t, "2115703436879081472", message.IDString)
}

func TestHistoryMessageIdentityRejectsMissingOrInconsistentForms(t *testing.T) {
	for _, body := range []string{
		`{"message_id":2115703436879081472,"message_idstr":"2115703436879081473"}`,
		`{"message_id":2115703436879081472}`,
		`{"message_idstr":"2115703436879081472"}`,
		`{"message_id":0,"message_idstr":"0"}`,
		`{"message_id":-1,"message_idstr":"-1"}`,
		`{"message_id":42,"message_idstr":"042"}`,
		`{"message_id":9223372036854775808,"message_idstr":"9223372036854775808"}`,
		`{"message_id":"42","message_idstr":"42"}`,
		`{"message_id":42.0,"message_idstr":"42"}`,
	} {
		var message messageFact
		err := json.Unmarshal([]byte(body), &message)
		if err == nil {
			err = message.validateIdentity()
		}
		require.Error(t, err, "invalid public identity must fail instead of being coerced: %s", body)
	}
}
