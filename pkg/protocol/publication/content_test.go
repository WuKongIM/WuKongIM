package publication_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

func TestPublicationContentEqualityExcludesOnlyIngressClock(t *testing.T) {
	original, err := publication.Encode(fixture())
	require.NoError(t, err)
	fingerprint, err := publication.ContentFingerprint(original)
	require.NoError(t, err)
	for _, tc := range []struct {
		name  string
		edit  func(*publication.Metadata)
		equal bool
	}{
		{"ingress", func(m *publication.Metadata) { m.AcceptedAtMS++ }, true},
		{"qos", func(m *publication.Metadata) { m.QoS = 0 }, false},
		{"source", func(m *publication.Metadata) { m.Source = publication.SourceWill; m.AcceptedAtMS = 0 }, false},
		{"namespace", func(m *publication.Metadata) { m.PublisherNamespace = "other" }, false},
		{"client", func(m *publication.Metadata) { m.PublisherClientID = "other" }, false},
		{"topic", func(m *publication.Metadata) { m.OriginalTopic = "other" }, false},
		{"order", func(m *publication.Metadata) { m.Properties[0], m.Properties[2] = m.Properties[2], m.Properties[0] }, false},
		{"property", func(m *publication.Metadata) { m.Properties[0].Value = "other" }, false},
		{"expiry", func(m *publication.Metadata) { m.Properties[1].Number++ }, false},
		{"absent_properties", func(m *publication.Metadata) { m.Properties = nil }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := fixture()
			tc.edit(&m)
			retry, err := publication.Encode(m)
			require.NoError(t, err)
			same, err := publication.SameContent(original, retry)
			require.NoError(t, err)
			require.Equal(t, tc.equal, same)
			retryFingerprint, err := publication.ContentFingerprint(retry)
			require.NoError(t, err)
			if tc.equal {
				require.Equal(t, fingerprint, retryFingerprint)
			} else {
				require.NotEqual(t, fingerprint, retryFingerprint, "fixture must exercise distinct lookup buckets")
			}
		})
	}
	for _, b := range [][]byte{nil, {}} {
		same, err := publication.SameContent(nil, b)
		require.NoError(t, err)
		require.True(t, same)
		same, err = publication.SameContent(original, b)
		require.NoError(t, err)
		require.False(t, same)
		fingerprint, err := publication.ContentFingerprint(b)
		require.NoError(t, err)
		require.Zero(t, fingerprint)
	}
	for _, bad := range [][]byte{{1}, {2}, make([]byte, publication.MaxEncodedBytes+1)} {
		_, err := publication.SameContent(bad, bad)
		require.Error(t, err)
		_, err = publication.SameContent(nil, bad)
		require.Error(t, err)
		_, err = publication.ContentFingerprint(bad)
		require.Error(t, err)
	}
}
