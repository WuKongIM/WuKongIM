package transfer

import (
	"bytes"
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// WalkMerge streams two disjoint prefixes in relative-key order using two
// pinned iterators and constant row memory. Equal suffixes are visited together;
// an absent side has an empty Key. Rows retain their full keys and owned bytes.
// Like Walk, the caller serializes operations, and callbacks may Get or Put
// outside the scanned prefixes. No goroutines or point lookups implement the join.
func (s *Spool) WalkMerge(ctx context.Context, leftPrefix, rightPrefix []byte, visit func(SpoolRow, SpoolRow) error) (err error) {
	if ctx == nil || visit == nil {
		return errors.New("migration spool requires context and visitor")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.db == nil {
		return errors.New("migration spool closed")
	}
	if len(leftPrefix) == 0 || len(rightPrefix) == 0 || bytes.HasPrefix(leftPrefix, rightPrefix) || bytes.HasPrefix(rightPrefix, leftPrefix) {
		return errors.New("migration spool merge requires disjoint nonempty prefixes")
	}
	left, err := s.db.NewIter(spoolPrefixSpan(leftPrefix), engine.IterOptions{})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, left.Close()) }()
	right, err := s.db.NewIter(spoolPrefixSpan(rightPrefix), engine.IterOptions{})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, right.Close()) }()
	leftOK, rightOK := left.First(), right.First()
	var leftKey, rightKey []byte
	for leftOK || rightOK {
		if err := errors.Join(ctx.Err(), left.Error(), right.Error()); err != nil {
			return err
		}
		if leftOK && leftKey == nil {
			leftKey = left.Key()
		}
		if rightOK && rightKey == nil {
			rightKey = right.Key()
		}
		order := 0
		switch {
		case !leftOK:
			order = 1
		case !rightOK:
			order = -1
		default:
			order = bytes.Compare(leftKey[len(leftPrefix):], rightKey[len(rightPrefix):])
		}
		var l, r SpoolRow
		if order <= 0 {
			value, err := left.Value()
			if err != nil {
				return err
			}
			l = SpoolRow{Key: leftKey, Value: value}
		}
		if order >= 0 {
			value, err := right.Value()
			if err != nil {
				return err
			}
			r = SpoolRow{Key: rightKey, Value: value}
		}
		if err := visit(l, r); err != nil {
			return err
		}
		if order <= 0 {
			leftOK, leftKey = left.Next(), nil
		}
		if order >= 0 {
			rightOK, rightKey = right.Next(), nil
		}
	}
	return errors.Join(left.Error(), right.Error())
}

func spoolPrefixSpan(prefix []byte) engine.Span {
	var end []byte
	for i := len(prefix) - 1; i >= 0; i-- {
		if prefix[i] != 255 {
			end = bytes.Clone(prefix[:i+1])
			end[i]++
			break
		}
	}
	return engine.Span{Start: prefix, End: end}
}
