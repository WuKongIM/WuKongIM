package proxy

import (
	"context"
	"errors"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestReadChannelRuntimeMetadataBatchRejectsUnboundedInput(t *testing.T) {
	keys := make([]metadb.ChannelKey, runtimeMetaBatchMaxReads+1)

	_, err := (*Store)(nil).ReadChannelRuntimeMetadataBatch(context.Background(), keys)

	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("ReadChannelRuntimeMetadataBatch() error = %v, want ErrInvalidArgument", err)
	}
}
