---
scope: package
summary: Classifies typed transient failures of authority-routed reads without HTTP or retry scheduling policy.
---

# Read Availability Flow

## Responsibility

Recognize temporary transport, admission and authority failures for read callers.
The entry adapter supplies the user-facing status/envelope; no retries run here.

## Boundaries

- Read-only classification depends on runtime error contracts, never on storage
  reads, global services, request fields or infrastructure construction.

## Main Flows

1. Reject caller cancellation, then inspect wrapped/joined typed causes.
2. Recognize network timeout or an exact known generic RPC cause.
3. Return a classification without modifying or retrying the failed operation.

## Invariants and Failure Semantics

- Wrapped/joined typed causes retain their identity. Generic RPC errors are
  recognized only when their complete message equals a known transient cause.
  The dedicated stale read-route cause is transient; database/CAS conflicts are not.
- Unknown text, permanent absence, invalid configuration and storage failures
  do not become retryable from substring matches. Caller cancellation is not
  retried; a read deadline can be retried by a caller with a new deadline.
- The classification is opt-in for ordinary message/conversation read failures;
  it does not alter CMD, mutation or batch response contracts.

## Read First

- [Classification](errors.go)
- [Local/RPC and negative cases](errors_test.go)

## Update Triggers

Update when the accepted causes, RPC compatibility rules or callers change.
