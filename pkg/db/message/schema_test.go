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

func TestMessageSchemaStoresCompleteMessageInOneFamily(t *testing.T) {
	rowFamily, ok := MessageTable.Family(messageHeaderFamilyID)
	if !ok {
		t.Fatal("row family missing")
	}
	if len(MessageTable.Families) != 1 {
		t.Fatalf("message families = %d, want 1", len(MessageTable.Families))
	}
	foundPayload := false
	for _, columnID := range rowFamily.Columns {
		if columnID == messageColumnIDPayload {
			foundPayload = true
			break
		}
	}
	if !foundPayload {
		t.Fatalf("row family columns = %v, want payload", rowFamily.Columns)
	}
}
