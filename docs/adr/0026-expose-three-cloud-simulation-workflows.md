---
status: accepted
---

# Expose three cloud simulation workflows

The repository will expose separate manual provisioning and analysis workflows plus a scheduled cleanup workflow. Provisioning selects a trusted source revision, Scenario Profile, Infrastructure Preset, bounded timing, region, and Cost Envelope, and returns an exact run identity. Analysis requires that exact identity, accepts an optional diagnostic focus and explicit Draft-PR permission, and never resolves an ambiguous `latest` run. Cleanup sweeps every 15 minutes for expired or simulator-less runs and also supports protected, approved early destruction by exact run identity. Provisioning summaries will make the identity and next analysis action easy to copy.
