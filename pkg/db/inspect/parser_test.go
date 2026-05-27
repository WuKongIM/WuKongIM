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

func TestParseRejectsQuotedCommandKeyword(t *testing.T) {
	_, err := Parse("'show' tables")
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("Parse() err = %v, want ErrInvalidQuery", err)
	}
}

func TestParseRejectsQuotedShowTables(t *testing.T) {
	_, err := Parse("show 'tables'")
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("Parse() err = %v, want ErrInvalidQuery", err)
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

func TestParseRejectsQuotedDescribeTable(t *testing.T) {
	_, err := Parse("describe 'meta.user'")
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("Parse() err = %v, want ErrInvalidQuery", err)
	}
}

func TestParseRejectsInvalidDescribeTable(t *testing.T) {
	_, err := Parse("describe ,")
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("Parse() err = %v, want ErrInvalidQuery", err)
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

func TestParseRejectsInvalidSelectTablePunctuation(t *testing.T) {
	_, err := Parse("select * from ,")
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("Parse() err = %v, want ErrInvalidQuery", err)
	}
}

func TestParseRejectsInvalidSelectTableClauseKeyword(t *testing.T) {
	_, err := Parse("select * from where")
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("Parse() err = %v, want ErrInvalidQuery", err)
	}
}

func TestParseRejectsInvalidSelectColumn(t *testing.T) {
	_, err := Parse("select = from meta.user")
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("Parse() err = %v, want ErrInvalidQuery", err)
	}
}

func TestParseRejectsQuotedFromKeyword(t *testing.T) {
	_, err := Parse("select uid, 'from' meta.user")
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("Parse() err = %v, want ErrInvalidQuery", err)
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

func TestParseRejectsQuotedWhereKeyword(t *testing.T) {
	_, err := Parse("select * from meta.user 'where' uid='u1'")
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("Parse() err = %v, want ErrInvalidQuery", err)
	}
}

func TestParseRejectsPunctuationCursor(t *testing.T) {
	_, err := Parse("select * from meta.user cursor ,")
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("Parse() err = %v, want ErrInvalidQuery", err)
	}
}

func TestParseAllowsUnsupportedKeywordColumnIdentifier(t *testing.T) {
	query, err := Parse("select order from meta.user")
	if err != nil {
		t.Fatalf("Parse() err = %v", err)
	}
	if !reflect.DeepEqual(query.Columns, []string{"order"}) {
		t.Fatalf("Columns = %#v, want order", query.Columns)
	}
}

func TestParseAllowsUnsupportedKeywordFilterIdentifier(t *testing.T) {
	query, err := Parse("select * from meta.user where order=1")
	if err != nil {
		t.Fatalf("Parse() err = %v", err)
	}
	if got, ok := query.Filters["order"].(int64); !ok || got != 1 {
		t.Fatalf("order filter = %#v, want int64(1)", query.Filters["order"])
	}
}

func TestParseRejectsUnsupportedJoin(t *testing.T) {
	_, err := Parse("select * from meta.user join meta.channel on uid=uid")
	if !errors.Is(err, ErrUnsupportedQuery) {
		t.Fatalf("Parse() err = %v, want ErrUnsupportedQuery", err)
	}
}

func TestParseRejectsUnsupportedGroupBy(t *testing.T) {
	_, err := Parse("select * from meta.user group by uid")
	if !errors.Is(err, ErrUnsupportedQuery) {
		t.Fatalf("Parse() err = %v, want ErrUnsupportedQuery", err)
	}
}

func TestParseRejectsUnsupportedOrderBy(t *testing.T) {
	_, err := Parse("select * from meta.user order by uid")
	if !errors.Is(err, ErrUnsupportedQuery) {
		t.Fatalf("Parse() err = %v, want ErrUnsupportedQuery", err)
	}
}

func TestParseRejectsUnsupportedOffset(t *testing.T) {
	_, err := Parse("select * from meta.user offset 10")
	if !errors.Is(err, ErrUnsupportedQuery) {
		t.Fatalf("Parse() err = %v, want ErrUnsupportedQuery", err)
	}
}

func TestParseRejectsDuplicateWhere(t *testing.T) {
	_, err := Parse("select * from meta.user where uid='u1' where token='t'")
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("Parse() err = %v, want ErrInvalidQuery", err)
	}
}

func TestParseRejectsDuplicateLimit(t *testing.T) {
	_, err := Parse("select * from meta.user limit 10 limit 20")
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("Parse() err = %v, want ErrInvalidQuery", err)
	}
}

func TestParseRejectsDuplicateCursor(t *testing.T) {
	_, err := Parse("select * from meta.user cursor 'a' cursor 'b'")
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("Parse() err = %v, want ErrInvalidQuery", err)
	}
}

func TestParseRejectsDuplicateFilterKey(t *testing.T) {
	_, err := Parse("select * from meta.user where uid='u1' and uid='u2'")
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("Parse() err = %v, want ErrInvalidQuery", err)
	}
}

func TestParseAllowsDifferentFilterKeys(t *testing.T) {
	query, err := Parse("select * from meta.user where uid='u1' and token='t'")
	if err != nil {
		t.Fatalf("Parse() err = %v", err)
	}
	if !reflect.DeepEqual(query.Filters, map[string]any{"uid": "u1", "token": "t"}) {
		t.Fatalf("Filters = %#v, want uid/token", query.Filters)
	}
}

func TestParseRejectsInvalidLiteral(t *testing.T) {
	_, err := Parse("select * from meta.user where uid=u1")
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("Parse() err = %v, want ErrInvalidQuery", err)
	}
}

func TestParseRejectsInvalidFilterKey(t *testing.T) {
	_, err := Parse("select * from meta.user where =1")
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("Parse() err = %v, want ErrInvalidQuery", err)
	}
}
