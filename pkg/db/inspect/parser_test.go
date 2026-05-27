package inspect

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseShowTables(t *testing.T) {
	query, err := Parse("show tables")
	if err != nil {
		t.Fatalf("Parse() err = %v", err)
	}
	if query.Kind != QueryShowTables {
		t.Fatalf("Kind = %v, want %v", query.Kind, QueryShowTables)
	}
}

func TestParseDescribeTable(t *testing.T) {
	query, err := Parse("describe meta.user")
	if err != nil {
		t.Fatalf("Parse() err = %v", err)
	}
	if query.Kind != QueryDescribe {
		t.Fatalf("Kind = %v, want %v", query.Kind, QueryDescribe)
	}
	if query.Table != "meta.user" {
		t.Fatalf("Table = %q, want meta.user", query.Table)
	}
}

func TestParseSelectWhereLimit(t *testing.T) {
	query, err := Parse("select uid, token from meta.user where uid='u1' limit 20")
	if err != nil {
		t.Fatalf("Parse() err = %v", err)
	}
	if query.Kind != QuerySelect {
		t.Fatalf("Kind = %v, want %v", query.Kind, QuerySelect)
	}
	if query.Table != "meta.user" {
		t.Fatalf("Table = %q, want meta.user", query.Table)
	}
	if !reflect.DeepEqual(query.Columns, []string{"uid", "token"}) {
		t.Fatalf("Columns = %#v, want uid/token", query.Columns)
	}
	if !reflect.DeepEqual(query.Filters, map[string]any{"uid": "u1"}) {
		t.Fatalf("Filters = %#v, want uid=u1", query.Filters)
	}
	if query.Limit != 20 {
		t.Fatalf("Limit = %d, want 20", query.Limit)
	}
}

func TestParseSelectNumericFilterAndCursor(t *testing.T) {
	query, err := Parse("select * from meta.channel where channel_type=2 cursor 'abc' limit 5")
	if err != nil {
		t.Fatalf("Parse() err = %v", err)
	}
	if !reflect.DeepEqual(query.Columns, []string{"*"}) {
		t.Fatalf("Columns = %#v, want *", query.Columns)
	}
	if got, ok := query.Filters["channel_type"].(int64); !ok || got != 2 {
		t.Fatalf("channel_type filter = %#v, want int64(2)", query.Filters["channel_type"])
	}
	if query.Cursor != "abc" {
		t.Fatalf("Cursor = %q, want abc", query.Cursor)
	}
	if query.Limit != 5 {
		t.Fatalf("Limit = %d, want 5", query.Limit)
	}
}

func TestParseAllowsUnsupportedKeywordStringLiteral(t *testing.T) {
	query, err := Parse("select * from meta.user where uid='join'")
	if err != nil {
		t.Fatalf("Parse() err = %v", err)
	}
	if !reflect.DeepEqual(query.Filters, map[string]any{"uid": "join"}) {
		t.Fatalf("Filters = %#v, want uid=join", query.Filters)
	}
}

func TestParseAllowsUnsupportedKeywordCursorLiteral(t *testing.T) {
	query, err := Parse("select * from meta.user cursor 'offset'")
	if err != nil {
		t.Fatalf("Parse() err = %v", err)
	}
	if query.Cursor != "offset" {
		t.Fatalf("Cursor = %q, want offset", query.Cursor)
	}
}

func TestParseRejectsUnsupportedJoin(t *testing.T) {
	_, err := Parse("select * from meta.user join meta.channel on uid=uid")
	if !errors.Is(err, ErrUnsupportedQuery) {
		t.Fatalf("Parse() err = %v, want ErrUnsupportedQuery", err)
	}
}

func TestParseRejectsInvalidLiteral(t *testing.T) {
	_, err := Parse("select * from meta.user where uid=u1")
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("Parse() err = %v, want ErrInvalidQuery", err)
	}
}
