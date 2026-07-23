package pluginhost

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
)

func TestLegacyRPCLoggerPanicPreservesPanicSemantics(t *testing.T) {
	logger := newLegacyRPCLogger(wklog.NewNop())

	require.PanicsWithValue(t, "request pool panic", func() {
		logger.Panic("request pool panic")
	})
}
