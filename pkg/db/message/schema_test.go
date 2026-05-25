package message

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

func TestMessageSchemaValidates(t *testing.T) {
	if err := schema.ValidateTable(MessageTable); err != nil {
		t.Fatalf("ValidateTable(MessageTable): %v", err)
	}
	if MessageTable.ID != TableIDMessage {
		t.Fatalf("MessageTable.ID = %d, want %d", MessageTable.ID, TableIDMessage)
	}
}

func TestMessageSchemaSeparatesPayloadFamily(t *testing.T) {
	payloadFamily, ok := MessageTable.Family(messagePayloadFamilyID)
	if !ok {
		t.Fatal("payload family missing")
	}
	if len(payloadFamily.Columns) != 1 || payloadFamily.Columns[0] != messageColumnIDPayload {
		t.Fatalf("payload family columns = %v", payloadFamily.Columns)
	}
}
