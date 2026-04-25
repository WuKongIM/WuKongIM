package raftlog

import "fmt"

type ScopeKind uint8

const (
	ScopeSlot       ScopeKind = 1
	ScopeController ScopeKind = 2
)

type Scope struct {
	Kind ScopeKind
	ID   uint64
}

func SlotScope(slotID uint64) Scope {
	return Scope{Kind: ScopeSlot, ID: slotID}
}

func ControllerScope() Scope {
	return Scope{Kind: ScopeController, ID: 1}
}

func (s Scope) String() string {
	switch s.Kind {
	case ScopeSlot:
		return fmt.Sprintf("slot/%d", s.ID)
	case ScopeController:
		return fmt.Sprintf("controller/%d", s.ID)
	default:
		return fmt.Sprintf("scope(%d)/%d", s.Kind, s.ID)
	}
}
