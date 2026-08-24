package message

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// SendBatchFailureDiagnostics explains how one failed item consumed the shared
// synchronous batch stages before the submitter returned a timeout.
type SendBatchFailureDiagnostics struct {
	// FailedStage is the bounded synchronous stage that returned the error.
	FailedStage string
	// Permission is the shared permission-stage duration for the batch.
	Permission time.Duration
	// PreAppend is the shared hook and person-directory duration for the batch.
	PreAppend time.Duration
	// Submitter is the shared channelappend submission duration for the batch.
	Submitter time.Duration
	// DeadlineBudgetBeforeSubmit is the item's remaining deadline when channelappend began.
	DeadlineBudgetBeforeSubmit time.Duration
}

type sendBatchFailureError struct {
	cause       error
	diagnostics SendBatchFailureDiagnostics
}

func (e *sendBatchFailureError) Error() string {
	return fmt.Sprintf(
		"message send batch %s failed (permission=%s pre_append=%s submitter=%s deadline_budget_before_submit=%s): %v",
		e.diagnostics.FailedStage,
		e.diagnostics.Permission,
		e.diagnostics.PreAppend,
		e.diagnostics.Submitter,
		e.diagnostics.DeadlineBudgetBeforeSubmit,
		e.cause,
	)
}

func (e *sendBatchFailureError) Unwrap() error { return e.cause }

// SendBatchFailureDiagnosticsFromError returns bounded stage timing carried by
// a failed SendBatch item while preserving errors.Is against the original cause.
func SendBatchFailureDiagnosticsFromError(err error) (SendBatchFailureDiagnostics, bool) {
	var target *sendBatchFailureError
	if !errors.As(err, &target) || target == nil {
		return SendBatchFailureDiagnostics{}, false
	}
	return target.diagnostics, true
}

func annotateSendBatchTimeout(err error, diagnostics SendBatchFailureDiagnostics) error {
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &sendBatchFailureError{cause: err, diagnostics: diagnostics}
}
