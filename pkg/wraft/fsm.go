package wraft

type FSM interface {
	Apply(a ToApply) error
}
