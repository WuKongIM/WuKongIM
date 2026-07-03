package controller

import "errors"

var (
	// ErrNodeLifecycleConflict reports that a node lifecycle request conflicts with existing membership state.
	ErrNodeLifecycleConflict = errors.New("controllerv2: node lifecycle conflict")
	// ErrNodeLifecycleNotFound reports that a node lifecycle request targets a missing node.
	ErrNodeLifecycleNotFound = errors.New("controllerv2: node lifecycle node not found")
)
