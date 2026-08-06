---
status: accepted
---

# Separate deployment from Cloud Lease lifecycle

WuKongIM activation will be a dedicated Deployment Action that consumes a non-secret Lease Receipt, immutable Deployment Bundle, Deployment Plan, and temporary SSH credential. It cannot acquire or release cloud resources; the top-level orchestrator owns those operations and reacts to a typed Deployment Receipt. This keeps reusable infrastructure lifecycle, product-specific deployment, and multi-stage workload policy independently testable and prevents deployment failure handling from becoming hidden provider mutation.
