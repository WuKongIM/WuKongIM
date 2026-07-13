---
status: accepted
---

# Use a repository-owned Go cloud control plane

GitHub workflows will invoke a repository-owned Go control-plane module and CLI for Simulation Run creation, status, analysis access, destruction, and sweeping. The control plane will recover run state from provider inventory and mandatory resource tags instead of relying on a workflow-local file or Terraform state. Provider-specific SDK behavior will remain behind Cloud Provider Adapters that can be tested with fakes; shell scripts and declarative infrastructure tools may support packaging or stable shared infrastructure but will not own the run lifecycle.
