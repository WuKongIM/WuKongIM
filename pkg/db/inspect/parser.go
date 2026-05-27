package inspect

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Parse parses the limited SQL subset supported by the inspect store.
func Parse(raw string) (Query, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, ";")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Query{}, ErrInvalidQuery
	}

	tokens, err := lex(raw)
	if err != nil {
		return Query{}, err
	}
	if len(tokens) == 0 {
		return Query{}, ErrInvalidQuery
	}

	if tokens[0].Quoted {
		return Query{}, ErrInvalidQuery
	}
	switch lower(tokens[0]) {
	case "show":
		if len(tokens) == 2 && !tokens[1].Quoted && lower(tokens[1]) == "tables" {
			return Query{Kind: QueryShowTables}, nil
		}
	case "describe", "desc":
		if len(tokens) == 2 && validTableIdentifier(tokens[1]) {
			return Query{Kind: QueryDescribe, Table: tokens[1].Text}, nil
		}
	case "select":
		return parseSelect(tokens)
	}
	return Query{}, ErrInvalidQuery
}

type token struct {
	Text   string
	Quoted bool
}

func lex(raw string) ([]token, error) {
	var tokens []token
	for i := 0; i < len(raw); {
		r := rune(raw[i])
		if unicode.IsSpace(r) {
			i++
			continue
		}
		switch raw[i] {
		case ',', '=':
			tokens = append(tokens, token{Text: raw[i : i+1]})
			i++
			continue
		case '\'':
			j := i + 1
			for j < len(raw) && raw[j] != '\'' {
				j++
			}
			if j >= len(raw) {
				return nil, ErrInvalidQuery
			}
			tokens = append(tokens, token{Text: raw[i+1 : j], Quoted: true})
			i = j + 1
			continue
		}

		j := i
		for j < len(raw) && !unicode.IsSpace(rune(raw[j])) && raw[j] != ',' && raw[j] != '=' && raw[j] != '\'' {
			j++
		}
		tokens = append(tokens, token{Text: raw[i:j]})
		i = j
	}
	return tokens, nil
}

func parseSelect(tokens []token) (Query, error) {
	from := -1
	for i := 1; i < len(tokens); i++ {
		if !tokens[i].Quoted && lower(tokens[i]) == "from" {
			from = i
			break
		}
	}
	if from <= 1 || from+1 >= len(tokens) {
		return Query{}, ErrInvalidQuery
	}

	columns, err := parseColumns(tokens[1:from])
	if err != nil {
		return Query{}, err
	}
	table := tokens[from+1].Text
	if !validTableIdentifier(tokens[from+1]) {
		return Query{}, ErrInvalidQuery
	}

	query := Query{
		Kind:    QuerySelect,
		Table:   table,
		Columns: columns,
		Filters: make(map[string]any),
	}
	var seenWhere, seenLimit, seenCursor bool
	for i := from + 2; i < len(tokens); {
		if tokens[i].Quoted {
			return Query{}, ErrInvalidQuery
		}
		switch lower(tokens[i]) {
		case "where":
			if seenWhere {
				return Query{}, ErrInvalidQuery
			}
			seenWhere = true
			next, err := parseWhere(tokens, i+1, query.Filters)
			if err != nil {
				return Query{}, err
			}
			i = next
		case "limit":
			if seenLimit {
				return Query{}, ErrInvalidQuery
			}
			seenLimit = true
			if i+1 >= len(tokens) || tokens[i+1].Quoted {
				return Query{}, ErrInvalidQuery
			}
			limit, err := strconv.Atoi(tokens[i+1].Text)
			if err != nil || limit <= 0 {
				return Query{}, ErrInvalidQuery
			}
			query.Limit = limit
			i += 2
		case "cursor":
			if seenCursor {
				return Query{}, ErrInvalidQuery
			}
			seenCursor = true
			if i+1 >= len(tokens) || tokens[i+1].Text == "" {
				return Query{}, ErrInvalidQuery
			}
			if !tokens[i+1].Quoted && !validNameIdentifier(tokens[i+1]) {
				return Query{}, ErrInvalidQuery
			}
			query.Cursor = tokens[i+1].Text
			i += 2
		default:
			if isUnsupportedSelectClause(tokens, i) {
				return Query{}, ErrUnsupportedQuery
			}
			return Query{}, ErrInvalidQuery
		}
	}
	return query, nil
}

func parseColumns(tokens []token) ([]string, error) {
	var columns []string
	expectColumn := true
	for _, tok := range tokens {
		if tok.Quoted {
			return nil, ErrInvalidQuery
		}
		if tok.Text == "," {
			if expectColumn {
				return nil, ErrInvalidQuery
			}
			expectColumn = true
			continue
		}
		if !expectColumn || tok.Text == "" {
			return nil, ErrInvalidQuery
		}
		if !validColumnIdentifier(tok) {
			return nil, ErrInvalidQuery
		}
		columns = append(columns, tok.Text)
		expectColumn = false
	}
	if expectColumn || len(columns) == 0 {
		return nil, ErrInvalidQuery
	}
	return columns, nil
}

func parseWhere(tokens []token, start int, filters map[string]any) (int, error) {
	i := start
	if i >= len(tokens) || isClause(tokens[i]) {
		return 0, ErrInvalidQuery
	}
	for {
		if i+2 >= len(tokens) || tokens[i].Quoted || tokens[i+1].Text != "=" {
			return 0, ErrInvalidQuery
		}
		key := tokens[i].Text
		if !validNameIdentifier(tokens[i]) {
			return 0, ErrInvalidQuery
		}
		if _, ok := filters[key]; ok {
			return 0, ErrInvalidQuery
		}
		value, err := parseLiteral(tokens[i+2])
		if err != nil {
			return 0, err
		}
		filters[key] = value
		i += 3
		if i >= len(tokens) || isClause(tokens[i]) || isUnsupportedSelectClause(tokens, i) {
			return i, nil
		}
		if lower(tokens[i]) != "and" {
			return 0, ErrInvalidQuery
		}
		i++
		if i >= len(tokens) || isClause(tokens[i]) {
			return 0, ErrInvalidQuery
		}
	}
}

func validTableIdentifier(tok token) bool {
	if tok.Quoted || tok.Text == "" || isClause(tok) || isUnsupportedClauseStarter(tok) {
		return false
	}
	segmentLen := 0
	for _, r := range tok.Text {
		if r == '.' {
			if segmentLen == 0 {
				return false
			}
			segmentLen = 0
			continue
		}
		if !isIdentifierRune(r) {
			return false
		}
		segmentLen++
	}
	return segmentLen > 0
}

func validColumnIdentifier(tok token) bool {
	if tok.Quoted {
		return false
	}
	if tok.Text == "*" {
		return true
	}
	return validNameIdentifier(tok)
}

func validNameIdentifier(tok token) bool {
	if tok.Quoted || tok.Text == "" {
		return false
	}
	for _, r := range tok.Text {
		if !isIdentifierRune(r) {
			return false
		}
	}
	return true
}

func isIdentifierRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_'
}

func isUnsupportedSelectClause(tokens []token, i int) bool {
	if i >= len(tokens) || tokens[i].Quoted {
		return false
	}
	switch lower(tokens[i]) {
	case "join", "offset":
		return true
	case "group", "order":
		return i+1 < len(tokens) && !tokens[i+1].Quoted && lower(tokens[i+1]) == "by"
	default:
		return false
	}
}

func isUnsupportedClauseStarter(tok token) bool {
	if tok.Quoted {
		return false
	}
	switch lower(tok) {
	case "join", "group", "order", "offset":
		return true
	default:
		return false
	}
}

func parseLiteral(tok token) (any, error) {
	if tok.Quoted {
		return tok.Text, nil
	}
	value, err := strconv.ParseInt(tok.Text, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid literal", ErrInvalidQuery)
	}
	return value, nil
}

func isClause(tok token) bool {
	if tok.Quoted {
		return false
	}
	switch lower(tok) {
	case "where", "limit", "cursor":
		return true
	default:
		return false
	}
}

func lower(tok token) string {
	return strings.ToLower(tok.Text)
}
